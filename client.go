package pingtunnel

import (
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/frame"
	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/gohome/network"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"io"
	"math"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	SEND_PROTO int = 8
	RECV_PROTO int = 0
)

func NewClient(addr string, server string, target string, timeout int, key int,
	tcpmode int, tcpmode_buffersize int, tcpmode_maxwin int, tcpmode_resend_timems int, tcpmode_compress int,
	tcpmode_stat int, open_sock5 int, maxconn int, sock5_filter *func(addr string) bool) (*Client, error) {

	var ipaddr *net.UDPAddr
	var tcpaddr *net.TCPAddr
	var err error

	if tcpmode > 0 {
		tcpaddr, err = net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return nil, err
		}
	} else {
		ipaddr, err = net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return nil, err
		}
	}

	ipaddrServer, err := net.ResolveIPAddr("ip", server)
	if err != nil {
		return nil, err
	}

	rand.Seed(time.Now().UnixNano())
	return &Client{
		exit:                  false,
		rtt:                   0,
		id:                    rand.Intn(math.MaxInt16),
		ipaddr:                ipaddr,
		tcpaddr:               tcpaddr,
		addr:                  addr,
		ipaddrServer:          ipaddrServer,
		addrServer:            server,
		targetAddr:            target,
		timeout:               timeout,
		key:                   key,
		tcpmode:               tcpmode,
		tcpmode_buffersize:    tcpmode_buffersize,
		tcpmode_maxwin:        tcpmode_maxwin,
		tcpmode_resend_timems: tcpmode_resend_timems,
		tcpmode_compress:      tcpmode_compress,
		tcpmode_stat:          tcpmode_stat,
		open_sock5:            open_sock5,
		maxconn:               maxconn,
		pongTime:              time.Now(),
		sock5_filter:          sock5_filter,
	}, nil
}

type Client struct {
	exit           bool
	rtt            time.Duration
	workResultLock sync.WaitGroup
	maxconn        int

	id       int
	sequence int

	timeout               int
	sproto                int
	rproto                int
	key                   int
	tcpmode               int
	tcpmode_buffersize    int
	tcpmode_maxwin        int
	tcpmode_resend_timems int
	tcpmode_compress      int
	tcpmode_stat          int

	open_sock5   int
	sock5_filter *func(addr string) bool

	ipaddr  *net.UDPAddr
	tcpaddr *net.TCPAddr
	addr    string

	ipaddrServer *net.IPAddr
	addrServer   string

	targetAddr string

	conn          *icmp.PacketConn
	listenConn    *net.UDPConn
	tcplistenConn *net.TCPListener

	localAddrToConnMap sync.Map
	localIdToConnMap   sync.Map

	sendPacket             uint64
	recvPacket             uint64
	sendPacketSize         uint64
	recvPacketSize         uint64
	localAddrToConnMapSize int
	localIdToConnMapSize   int

	recvcontrol chan int

	pongTime time.Time
}

type ClientConn struct {
	exit           bool
	ipaddr         *net.UDPAddr
	tcpaddr        *net.TCPAddr
	id             string
	activeRecvTime time.Time
	activeSendTime time.Time
	close          bool

	fm *frame.FrameMgr
}

func (p *Client) Addr() string {
	return p.addr
}

func (p *Client) IPAddr() *net.UDPAddr {
	return p.ipaddr
}

func (p *Client) TargetAddr() string {
	return p.targetAddr
}

func (p *Client) ServerIPAddr() *net.IPAddr {
	return p.ipaddrServer
}

func (p *Client) ServerAddr() string {
	return p.addrServer
}

func (p *Client) RTT() time.Duration {
	return p.rtt
}

func (p *Client) RecvPacketSize() uint64 {
	return p.recvPacketSize
}

func (p *Client) SendPacketSize() uint64 {
	return p.sendPacketSize
}

func (p *Client) RecvPacket() uint64 {
	return p.recvPacket
}

func (p *Client) SendPacket() uint64 {
	return p.sendPacket
}

func (p *Client) LocalIdToConnMapSize() int {
	return p.localIdToConnMapSize
}

func (p *Client) LocalAddrToConnMapSize() int {
	return p.localAddrToConnMapSize
}

func (p *Client) Run() error {

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		loggo.Error("Error listening for ICMP packets: %s", err.Error())
		return err
	}
	p.conn = conn

	if p.tcpmode > 0 {
		tcplistenConn, err := net.ListenTCP("tcp", p.tcpaddr)
		if err != nil {
			loggo.Error("Error listening for tcp packets: %s", err.Error())
			return err
		}
		p.tcplistenConn = tcplistenConn
	} else {
		listener, err := net.ListenUDP("udp", p.ipaddr)
		if err != nil {
			loggo.Error("Error listening for udp packets: %s", err.Error())
			return err
		}
		p.listenConn = listener
	}

	if p.tcpmode > 0 {
		go p.AcceptTcp()
	} else {
		go p.Accept()
	}

	recv := make(chan *Packet, 10000)
	p.recvcontrol = make(chan int, 1)
	go recvICMP(&p.workResultLock, &p.exit, *p.conn, recv)

	go func() {
		defer common.CrashLog()

		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			p.checkTimeoutConn()
			p.ping()
			p.showNet()
			time.Sleep(time.Second)
		}
	}()

	go func() {
		defer common.CrashLog()

		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			p.updateServerAddr()
			time.Sleep(time.Second)
		}
	}()

	go func() {
		defer common.CrashLog()

		p.workResultLock.Add(1)
		defer p.workResultLock.Done()

		for !p.exit {
			select {
			case <-p.recvcontrol:
				return
			case r := <-recv:
				p.processPacket(r)
			}
		}
	}()

	return nil
}

func (p *Client) Stop() {
	p.exit = true
	p.recvcontrol <- 1
	p.workResultLock.Wait()
	p.conn.Close()
	if p.tcplistenConn != nil {
		p.tcplistenConn.Close()
	}
	if p.listenConn != nil {
		p.listenConn.Close()
	}
}

func (p *Client) AcceptTcp() error {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("client waiting local accept tcp")

	for !p.exit {
		p.tcplistenConn.SetDeadline(time.Now().Add(time.Millisecond * 1000))

		conn, err := p.tcplistenConn.AcceptTCP()
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error accept tcp %s", err)
				continue
			}
		}

		if conn != nil {
			if p.open_sock5 > 0 {
				go p.AcceptSock5Conn(conn)
			} else {
				go p.AcceptTcpConn(conn, p.targetAddr)
			}
		}
	}
	return nil
}

func (p *Client) AcceptTcpConn(conn *net.TCPConn, targetAddr string) {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	tcpsrcaddr := conn.RemoteAddr().(*net.TCPAddr)

	if p.maxconn > 0 && p.localIdToConnMapSize >= p.maxconn {
		loggo.Info("too many connections %d, client accept new local tcp fail %s", p.localIdToConnMapSize, tcpsrcaddr.String())
		return
	}

	uuid := common.UniqueId()

	fm := frame.NewFrameMgr(FRAME_MAX_SIZE, FRAME_MAX_ID, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems, p.tcpmode_compress, p.tcpmode_stat)

	now := time.Now()
	clientConn := &ClientConn{exit: false, tcpaddr: tcpsrcaddr, id: uuid, activeRecvTime: now, activeSendTime: now, close: false,
		fm: fm}
	p.addClientConn(uuid, tcpsrcaddr.String(), clientConn)
	loggo.Info("client accept new local tcp %s %s", uuid, tcpsrcaddr.String())

	loggo.Info("start connect remote tcp %s %s", uuid, tcpsrcaddr.String())
	clientConn.fm.Connect()
	startConnectTime := common.GetNowUpdateInSecond()
	for !p.exit && !clientConn.exit {
		if clientConn.fm.IsConnected() {
			break
		}
		clientConn.fm.Update()
		sendlist := clientConn.fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, _ := clientConn.fm.MarshalFrame(f)
			p.sequence++
			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
				SEND_PROTO, RECV_PROTO, p.key,
				p.tcpmode, p.tcpmode_buffersize, p.tcpmode_maxwin, p.tcpmode_resend_timems, p.tcpmode_compress, p.tcpmode_stat,
				p.timeout)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}
		time.Sleep(time.Millisecond * 10)
		now := common.GetNowUpdateInSecond()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Second*5 {
			loggo.Info("can not connect remote tcp %s %s", uuid, tcpsrcaddr.String())
			p.close(clientConn)
			return
		}
	}

	if !clientConn.exit {
		loggo.Info("connected remote tcp %s %s", uuid, tcpsrcaddr.String())
	}

	bytes := make([]byte, 10240)

	tcpActiveRecvTime := common.GetNowUpdateInSecond()
	tcpActiveSendTime := common.GetNowUpdateInSecond()

	for !p.exit && !clientConn.exit {
		now := common.GetNowUpdateInSecond()
		sleep := true

		left := common.MinOfInt(clientConn.fm.GetSendBufferLeft(), len(bytes))
		if left > 0 {
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.Read(bytes[0:left])
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error read tcp %s %s %s", uuid, tcpsrcaddr.String(), err)
					clientConn.fm.Close()
					break
				}
			}
			if n > 0 {
				sleep = false
				clientConn.fm.WriteSendBuffer(bytes[:n])
				tcpActiveRecvTime = now
			}
		}

		clientConn.fm.Update()

		sendlist := clientConn.fm.GetSendList()
		if sendlist.Len() > 0 {
			sleep = false
			clientConn.activeSendTime = now
			for e := sendlist.Front(); e != nil; e = e.Next() {
				f := e.Value.(*frame.Frame)
				mb, err := clientConn.fm.MarshalFrame(f)
				if err != nil {
					loggo.Error("Error tcp Marshal %s %s %s", uuid, tcpsrcaddr.String(), err)
					continue
				}
				p.sequence++
				sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
					SEND_PROTO, RECV_PROTO, p.key,
					p.tcpmode, 0, 0, 0, 0, 0,
					0)
				p.sendPacket++
				p.sendPacketSize += (uint64)(len(mb))
			}
		}

		if clientConn.fm.GetRecvBufferSize() > 0 {
			sleep = false
			rr := clientConn.fm.GetRecvReadLineBuffer()
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 1))
			n, err := conn.Write(rr)
			if err != nil {
				nerr, ok := err.(net.Error)
				if !ok || !nerr.Timeout() {
					loggo.Info("Error write tcp %s %s %s", uuid, tcpsrcaddr.String(), err)
					clientConn.fm.Close()
					break
				}
			}
			if n > 0 {
				clientConn.fm.SkipRecvBuffer(n)
				tcpActiveSendTime = now
			}
		}

		if sleep {
			time.Sleep(time.Millisecond * 10)
		}

		diffrecv := now.Sub(clientConn.activeRecvTime)
		diffsend := now.Sub(clientConn.activeSendTime)
		tcpdiffrecv := now.Sub(tcpActiveRecvTime)
		tcpdiffsend := now.Sub(tcpActiveSendTime)
		if diffrecv > time.Second*(time.Duration(p.timeout)) || diffsend > time.Second*(time.Duration(p.timeout)) ||
			(tcpdiffrecv > time.Second*(time.Duration(p.timeout)) && tcpdiffsend > time.Second*(time.Duration(p.timeout))) {
			loggo.Info("close inactive conn %s %s", clientConn.id, clientConn.tcpaddr.String())
			clientConn.fm.Close()
			break
		}

		if clientConn.fm.IsRemoteClosed() {
			loggo.Info("closed by remote conn %s %s", clientConn.id, clientConn.tcpaddr.String())
			clientConn.fm.Close()
			break
		}
	}

	clientConn.fm.Close()

	startCloseTime := common.GetNowUpdateInSecond()
	for !p.exit && !clientConn.exit {
		now := common.GetNowUpdateInSecond()

		clientConn.fm.Update()

		sendlist := clientConn.fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, _ := clientConn.fm.MarshalFrame(f)
			p.sequence++
			sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, targetAddr, clientConn.id, (uint32)(MyMsg_DATA), mb,
				SEND_PROTO, RECV_PROTO, p.key,
				p.tcpmode, 0, 0, 0, 0, 0,
				0)
			p.sendPacket++
			p.sendPacketSize += (uint64)(len(mb))
		}

		nodatarecv := true
		if clientConn.fm.GetRecvBufferSize() > 0 {
			rr := clientConn.fm.GetRecvReadLineBuffer()
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			n, _ := conn.Write(rr)
			if n > 0 {
				clientConn.fm.SkipRecvBuffer(n)
				nodatarecv = false
			}
		}

		diffclose := now.Sub(startCloseTime)
		if diffclose > time.Second*60 {
			loggo.Info("close conn had timeout %s %s", clientConn.id, clientConn.tcpaddr.String())
			break
		}

		remoteclosed := clientConn.fm.IsRemoteClosed()
		if remoteclosed && nodatarecv {
			loggo.Info("remote conn had closed %s %s", clientConn.id, clientConn.tcpaddr.String())
			break
		}

		time.Sleep(time.Millisecond * 100)
	}

	loggo.Info("close tcp conn %s %s", clientConn.id, clientConn.tcpaddr.String())
	conn.Close()
	p.close(clientConn)
}

func (p *Client) Accept() error {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	loggo.Info("client waiting local accept udp")

	bytes := make([]byte, 10240)

	for !p.exit {
		p.listenConn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := p.listenConn.ReadFromUDP(bytes)
		if err != nil {
			nerr, ok := err.(net.Error)
			if !ok || !nerr.Timeout() {
				loggo.Info("Error read udp %s", err)
				continue
			}
		}
		if n <= 0 {
			continue
		}

		now := common.GetNowUpdateInSecond()
		clientConn := p.getClientConnByAddr(srcaddr.String())
		if clientConn == nil {
			if p.maxconn > 0 && p.localIdToConnMapSize >= p.maxconn {
				loggo.Info("too many connections %d, client accept new local udp fail %s", p.localIdToConnMapSize, srcaddr.String())
				continue
			}
			uuid := common.UniqueId()
			clientConn = &ClientConn{exit: false, ipaddr: srcaddr, id: uuid, activeRecvTime: now, activeSendTime: now, close: false}
			p.addClientConn(uuid, srcaddr.String(), clientConn)
			loggo.Info("client accept new local udp %s %s", uuid, srcaddr.String())
		}

		clientConn.activeSendTime = now
		sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, p.targetAddr, clientConn.id, (uint32)(MyMsg_DATA), bytes[:n],
			SEND_PROTO, RECV_PROTO, p.key,
			p.tcpmode, 0, 0, 0, 0, 0,
			p.timeout)

		p.sequence++

		p.sendPacket++
		p.sendPacketSize += (uint64)(n)
	}
	return nil
}

func (p *Client) processPacket(packet *Packet) {

	if packet.my.Rproto >= 0 {
		return
	}

	if packet.my.Key != (int32)(p.key) {
		return
	}

	if packet.echoId != p.id {
		return
	}

	if packet.my.Type == (int32)(MyMsg_PING) {
		t := time.Time{}
		t.UnmarshalBinary(packet.my.Data)
		now := time.Now()
		d := now.Sub(t)
		loggo.Info("pong from %s %s", packet.src.String(), d.String())
		p.rtt = d
		p.pongTime = now
		return
	}

	if packet.my.Type == (int32)(MyMsg_KICK) {
		clientConn := p.getClientConnById(packet.my.Id)
		if clientConn != nil {
			p.close(clientConn)
			loggo.Info("remote kick local %s", packet.my.Id)
		}
		return
	}

	loggo.Debug("processPacket %s %s %d", packet.my.Id, packet.src.String(), len(packet.my.Data))

	clientConn := p.getClientConnById(packet.my.Id)
	if clientConn == nil {
		loggo.Debug("processPacket no conn %s ", packet.my.Id)
		p.remoteError(packet.my.Id)
		return
	}

	now := common.GetNowUpdateInSecond()
	clientConn.activeRecvTime = now

	if p.tcpmode > 0 {
		f := &frame.Frame{}
		err := proto.Unmarshal(packet.my.Data, f)
		if err != nil {
			loggo.Error("Unmarshal tcp Error %s", err)
			return
		}

		clientConn.fm.OnRecvFrame(f)
	} else {
		if packet.my.Data == nil {
			return
		}
		addr := clientConn.ipaddr
		_, err := p.listenConn.WriteToUDP(packet.my.Data, addr)
		if err != nil {
			loggo.Info("WriteToUDP Error read udp %s", err)
			clientConn.close = true
			return
		}
	}

	p.recvPacket++
	p.recvPacketSize += (uint64)(len(packet.my.Data))
}

func (p *Client) close(clientConn *ClientConn) {
	clientConn.exit = true
	p.deleteClientConn(clientConn.id, clientConn.ipaddr.String())
	p.deleteClientConn(clientConn.id, clientConn.tcpaddr.String())
}

func (p *Client) checkTimeoutConn() {

	if p.tcpmode > 0 {
		return
	}

	tmp := make(map[string]*ClientConn)
	p.localIdToConnMap.Range(func(key, value interface{}) bool {
		id := key.(string)
		clientConn := value.(*ClientConn)
		tmp[id] = clientConn
		return true
	})

	now := common.GetNowUpdateInSecond()
	for _, conn := range tmp {
		diffrecv := now.Sub(conn.activeRecvTime)
		diffsend := now.Sub(conn.activeSendTime)
		if diffrecv > time.Second*(time.Duration(p.timeout)) || diffsend > time.Second*(time.Duration(p.timeout)) {
			conn.close = true
		}
	}

	for id, conn := range tmp {
		if conn.close {
			loggo.Info("close inactive conn %s %s", id, conn.ipaddr.String())
			p.close(conn)
		}
	}
}

func (p *Client) ping() {
	now := time.Now()
	b, _ := now.MarshalBinary()
	sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, "", "", (uint32)(MyMsg_PING), b,
		SEND_PROTO, RECV_PROTO, p.key,
		0, 0, 0, 0, 0, 0,
		0)
	loggo.Info("ping %s %s %d %d %d %d", p.addrServer, now.String(), p.sproto, p.rproto, p.id, p.sequence)
	p.sequence++
	if now.Sub(p.pongTime) > time.Second*3 {
		p.rtt = 0
	}
}

func (p *Client) showNet() {
	p.localAddrToConnMapSize = 0
	p.localIdToConnMap.Range(func(key, value interface{}) bool {
		p.localAddrToConnMapSize++
		return true
	})
	p.localIdToConnMapSize = 0
	p.localIdToConnMap.Range(func(key, value interface{}) bool {
		p.localIdToConnMapSize++
		return true
	})
	loggo.Info("send %dPacket/s %dKB/s recv %dPacket/s %dKB/s %d/%dConnections",
		p.sendPacket, p.sendPacketSize/1024, p.recvPacket, p.recvPacketSize/1024, p.localAddrToConnMapSize, p.localIdToConnMapSize)
	p.sendPacket = 0
	p.recvPacket = 0
	p.sendPacketSize = 0
	p.recvPacketSize = 0
}

func (p *Client) AcceptSock5Conn(conn *net.TCPConn) {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	var err error = nil
	if err = network.Sock5HandshakeBy(conn, "", ""); err != nil {
		loggo.Error("socks handshake: %s", err)
		conn.Close()
		return
	}
	_, addr, err := network.Sock5GetRequest(conn)
	if err != nil {
		loggo.Error("error getting request: %s", err)
		conn.Close()
		return
	}
	// Sending connection established message immediately to client.
	// This some round trip time for creating socks connection with the client.
	// But if connection failed, the client will get connection reset error.
	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x08, 0x43})
	if err != nil {
		loggo.Error("send connection confirmation: %s", err)
		conn.Close()
		return
	}

	loggo.Info("accept new sock5 conn: %s", addr)

	if p.sock5_filter == nil {
		p.AcceptTcpConn(conn, addr)
	} else {
		if (*p.sock5_filter)(addr) {
			p.AcceptTcpConn(conn, addr)
			return
		}
		p.AcceptDirectTcpConn(conn, addr)
	}
}

func (p *Client) addClientConn(uuid string, addr string, clientConn *ClientConn) {

	p.localAddrToConnMap.Store(addr, clientConn)
	p.localIdToConnMap.Store(uuid, clientConn)
}

func (p *Client) getClientConnByAddr(addr string) *ClientConn {
	ret, ok := p.localAddrToConnMap.Load(addr)
	if !ok {
		return nil
	}
	return ret.(*ClientConn)
}

func (p *Client) getClientConnById(uuid string) *ClientConn {
	ret, ok := p.localIdToConnMap.Load(uuid)
	if !ok {
		return nil
	}
	return ret.(*ClientConn)
}

func (p *Client) deleteClientConn(uuid string, addr string) {
	p.localIdToConnMap.Delete(uuid)
	p.localAddrToConnMap.Delete(addr)
}

func (p *Client) remoteError(uuid string) {
	sendICMP(p.id, p.sequence, *p.conn, p.ipaddrServer, "", uuid, (uint32)(MyMsg_KICK), []byte{},
		SEND_PROTO, RECV_PROTO, p.key,
		0, 0, 0, 0, 0, 0,
		0)
}

func (p *Client) AcceptDirectTcpConn(conn *net.TCPConn, targetAddr string) {

	defer common.CrashLog()

	p.workResultLock.Add(1)
	defer p.workResultLock.Done()

	tcpsrcaddr := conn.RemoteAddr().(*net.TCPAddr)

	loggo.Info("client accept new direct local tcp %s %s", tcpsrcaddr.String(), targetAddr)

	tcpaddrTarget, err := net.ResolveTCPAddr("tcp", targetAddr)
	if err != nil {
		loggo.Info("direct local tcp ResolveTCPAddr fail: %s %s", targetAddr, err.Error())
		return
	}

	targetconn, err := net.DialTCP("tcp", nil, tcpaddrTarget)
	if err != nil {
		loggo.Info("direct local tcp DialTCP fail: %s %s", targetAddr, err.Error())
		return
	}

	go p.transfer(conn, targetconn, conn.RemoteAddr().String(), targetconn.RemoteAddr().String())
	go p.transfer(targetconn, conn, targetconn.RemoteAddr().String(), conn.RemoteAddr().String())

	loggo.Info("client accept new direct local tcp ok %s %s", tcpsrcaddr.String(), targetAddr)
}

func (p *Client) transfer(destination io.WriteCloser, source io.ReadCloser, dst string, src string) {

	defer common.CrashLog()

	defer destination.Close()
	defer source.Close()
	loggo.Info("client begin transfer from %s -> %s", src, dst)
	io.Copy(destination, source)
	loggo.Info("client end transfer from %s -> %s", src, dst)
}

func (p *Client) updateServerAddr() {
	ipaddrServer, err := net.ResolveIPAddr("ip", p.addrServer)
	if err != nil {
		return
	}
	if p.ipaddrServer.String() != ipaddrServer.String() {
		p.ipaddrServer = ipaddrServer
	}
}
