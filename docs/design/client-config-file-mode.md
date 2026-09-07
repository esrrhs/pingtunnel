# 设计与实现：客户端配置文件模式（单进程多端口转发）

日期：2026-09-07
状态：已实现并通过测试

## 1. 背景与目标

客户端此前的用法是每个进程转发一条规则：

```
pingtunnel.exe -type client -l :16122 -s SERVER -t 192.168.0.61:22 -tcp 1
```

需要同时转发多条端口映射时（例如本机 16122/16222/16322 分别对应内网 61/62/63 的 SSH），就必须启动多个 pingtunnel 进程，管理成本高（多个窗口、多处开机自启、互相看不出关系）。

目标：新增 `-c <config.yaml>` 模式，一个进程并发处理任意多条转发规则：

```
pingtunnel.exe -type client -c config.yaml
```

非目标：不改变现有单条命令行用法（完全向后兼容）；服务端不需要任何修改（它本来就按连接动态转发任意目标）；不做规则热更新。

## 2. 方案选型

考虑了两个方案：

| 方案 | 做法 | 取舍 |
|---|---|---|
| A. 进程内多 Client 实例（采用） | 每条规则一个 `NewClient` + `Run`，实例各自持有 ICMP socket | 完全复用现有 Client 代码，改动集中在 main 与配置解析；每实例多开一个 ICMP socket、各自 ping |
| B. 单 Client 支持多 listener | 重构 Client 为多规则结构，共享一个 ICMP socket | 侵入 AcceptTcp/Accept/processPacket 主干，回归风险大，收益只是省两个 socket |

选 A。多实例的代价（每实例一个 raw ICMP socket、各自 ping、Windows 把所有回包副本投给每个 socket 再按 echo id 过滤）实测可忽略，正确性由 echo id 唯一性保证（见 §4.4）。

## 3. 配置文件格式

yaml 字符串列表，每行一条规则，**参数写法与命令行完全一致**（用户可把现有单条命令直接粘贴）：

```yaml
- -l :16122 -s 36.137.45.170 -t 192.168.0.61:22 -tcp 1
- -l :16222 -s 36.137.45.170 -t 192.168.0.62:22 -tcp 1
- -l :16322 -s 36.137.45.170 -t 192.168.0.63:22 -tcp 1
```

socks5 规则（无 `-t`）可混用。

## 4. 设计细节

### 4.1 参数分层

- **规则级**（每条规则独立）：`-l -s -t -timeout -key -tcp -tcp_bs -tcp_mw -tcp_rst -tcp_gz -tcp_stat -sock5 -s5user -s5pass -maxconn`
- **进程级**（只在命令行生效，规则里出现时被接受但忽略）：`-noprint -nolog -loglevel -profile -encrypt -encrypt-key -icmp_l -s5filter -s5ftfile` 及 server 专属参数。设计动机：`-encrypt/-encrypt-key` 全局统一才能与服务端匹配；loggo 只能初始化一次。

### 4.2 默认值继承

规则的参数默认值 = 命令行解析结果（`ClientFlags.toRule()`）。命令行上设置的参数（如 `-key 7 -timeout 120`）成为所有规则的默认，规则里显式写的优先。命令行未设置时回落到内置默认（timeout 60、tcp_bs 1MB、tcp_mw 20000 等，即 `builtinClientRule`），保证与原版单条模式行为一致。

### 4.3 启动失败回滚

解析阶段校验（缺 `-l/-s/-t`、监听端口重复、`-tcp_mw` 超限、未知参数即报错）之后逐条启动；任一规则 `NewClient`/`Run` 失败时，**停止已启动的全部实例**再退出，避免半可用状态。

### 4.4 多实例正确性：echo id 唯一分配

`Client` 的 ICMP echo id 原实现为随机数（`rand.Intn(MaxInt16)`）。多实例下两个实例若撞 id，回包会被对方实例的 echoId 过滤放行、因连接 id 查不到而触发 `remoteError` → KICK，在服务端互踢连接。因此：

- 包级注册表 `usedClientIDs sync.Map`：`NewClient` 用 `LoadOrStore` 抽签保证进程内 id 唯一，`Stop` 释放；
- `Stop` 增加"从未 Run 的实例安全停止"路径（原实现会对 nil channel 发送导致死锁、对 nil conn 解引用 panic）；
- 顺手移除已废弃且在 Go 1.24+ 为空操作的 `rand.Seed`。

### 4.5 通道隔离分析（用户关注的安全性）

本地端口→实例是 OS 级独占绑定（第二实例绑同端口会失败，即 §4.3 的校验兜底）；每连接 id 为 `crypto/rand` 128 位随机数（`common.UniqueId`），服务端按连接 id 建立并绑定各自的转发目标，回包按 id 精确匹配回本地连接，查不到即丢弃——不存在"一个端口的流量写到另一个目标"的路径。ICMP 层再有 echoId 过滤兜底。

## 5. 实现清单

| 文件 | 职责 |
|---|---|
| `cmd/client_flags.go` | 新增。`clientRule`（规则参数结构）、`ClientFlags`（全部 flag 指针）、`newClientFlagSet`（统一注册，defaults 参数实现默认值继承）、`parseClientRule`（单条规则解析，进程级参数接受但忽略） |
| `cmd/config.go` | 新增。`loadClientRules`（读 yaml、逐条解析校验、监听端口查重） |
| `cmd/main.go` | 加 `-c` flag 与 usage 说明；flag 定义统一走 `newClientFlagSet`；`runClientsFromConfig` 逐条创建并 `Run`，失败回滚 |
| `client.go` | §4.4 的 echo id 唯一分配与 Stop 加固 |
| `cmd/config_test.go` | 14 个用例：多规则、进程级参数忽略、默认值继承/回落、socks5 强制 tcpmode、缺参/重复/未知参数/超窗/空文件/非列表/文件不存在 |
| `client_id_test.go` | id 唯一性（200 实例）与 Stop 释放 |
| `config.yaml.example` | 示例配置 |
| `README.md` | 用法说明 |
| `go.mod` | 新增 `gopkg.in/yaml.v3`（纯 Go、无传递依赖） |

## 6. 踩坑记录

**tcp_mw 清零 bug（端到端测试才暴露）**：单条路径有"`-tcp` 未开启则清零缓冲参数"的逻辑，初版实现把它放在了 client 分支的公共位置，导致 `-c` 模式下命令行未传 `-tcp` 时，`tcp_mw=20000`/`tcp_bs=1MB` 被清零后又作为默认值继承给规则 → 每条规则的 `FrameMgr` 窗口大小为 0 → `Connect()` 立即失败（`sendwin.Size() >= windowsize` 恒真）→ 所有 TCP 转发无法建立。ICMP ping/pong 正常，极具迷惑性。修复：清零逻辑收回单条路径，`-c` 路径按每条规则自身的 `-tcp` 独立处理。教训：跨路径共享的默认值结构，任何"预设处理"都必须只作用于自己的路径。

## 7. 测试与验证

- `go build ./...`、`go test ./...`（根包 + cmd 包）全部通过；
- 端到端（Windows + 真实服务端 36.137.45.170，内网目标 192.168.0.61/62/63 的 SSH）：
  - 三规则并发启动，`Client[1..3]` 各自监听；
  - 三个端口 ssh 均收到目标机器 SSH 横幅，`connected remote tcp` ×3，`pong` RTT ~6ms；
  - 故障注入：占用某规则的监听端口 → 该规则报错、已启动规则回滚、进程退出；
  - 长会话 ssh 操作正常。

## 8. 使用指南

```
# config.yaml（每行一条规则，参数与命令行一致）
- -l :16122 -s 36.137.45.170 -t 192.168.0.61:22 -tcp 1
- -l :16222 -s 36.137.45.170 -t 192.168.0.62:22 -tcp 1
- -l :16322 -s 36.137.45.170 -t 192.168.0.63:22 -tcp 1

# 启动（Windows 需管理员权限，raw ICMP socket）
pingtunnel.exe -type client -c config.yaml

# 静默运行
pingtunnel.exe -type client -c config.yaml -noprint 1 -nolog 1

# 命令行参数作为所有规则的默认值，例如
pingtunnel.exe -type client -c config.yaml -key 7 -timeout 120
```

注意：`config.yaml` 含真实环境地址，默认被 `.gitignore` 忽略，请勿提交；模板见 `config.yaml.example`。

## 9. 已知限制与后续方向

- 每实例一个 ICMP socket、各自发 ping：服务端日志会看到同一来源多个 ping 源（不影响功能）；每个实例收到所有回包副本后按 echoId 过滤，CPU 开销可忽略。
- 无规则热更新：改配置需重启进程。
- 无优雅退出（Ctrl+C 直接结束进程），与原版行为一致；如需后续可加信号处理统一 `Stop` 全部实例。
- echo id 分配理论上限 32767 个并发实例，远超实际场景。
