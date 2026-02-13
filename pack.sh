#! /bin/bash
#set -x
NAME="pingtunnel"

export GO111MODULE=on

resolve_ndk_host_tag() {
  local host_os
  local host_arch
  host_os=$(uname -s)
  host_arch=$(uname -m)

  case "${host_os}/${host_arch}" in
    Linux/x86_64)
      echo "linux-x86_64"
      ;;
    Darwin/x86_64)
      echo "darwin-x86_64"
      ;;
    Darwin/arm64)
      echo "darwin-arm64"
      ;;
    *)
      return 1
      ;;
  esac
}

prepare_android_toolchain() {
  local arch="$1"
  local ndk_home
  local host_tag
  local api_level
  local target

  ndk_home="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
  if [ -z "$ndk_home" ]; then
    echo "ANDROID_NDK_HOME or ANDROID_NDK_ROOT must be set for Android builds"
    return 1
  fi

  host_tag=$(resolve_ndk_host_tag) || {
    echo "Unsupported host for Android NDK toolchain: $(uname -s)/$(uname -m)"
    return 1
  }

  api_level="${ANDROID_API_LEVEL:-21}"
  case "$arch" in
    386)
      target="i686-linux-android"
      ;;
    amd64)
      target="x86_64-linux-android"
      ;;
    arm)
      target="armv7a-linux-androideabi"
      ;;
    arm64)
      target="aarch64-linux-android"
      ;;
    *)
      echo "Unsupported Android arch: $arch"
      return 1
      ;;
  esac

  ANDROID_CC="$ndk_home/toolchains/llvm/prebuilt/$host_tag/bin/${target}${api_level}-clang"
  ANDROID_CXX="$ndk_home/toolchains/llvm/prebuilt/$host_tag/bin/${target}${api_level}-clang++"

  if [ ! -x "$ANDROID_CC" ] || [ ! -x "$ANDROID_CXX" ]; then
    echo "Android toolchain not found for $arch at $ndk_home"
    return 1
  fi

  return 0
}

#go tool dist list
build_list=$(go tool dist list)

go mod tidy

cd cmd/

rm pack -rf
rm pack.zip -f
mkdir pack

for line in $build_list; do
  os=$(echo "$line" | awk -F"/" '{print $1}')
  arch=$(echo "$line" | awk -F"/" '{print $2}')
  echo "os="$os" arch="$arch" start build"
  if [ "$os" == "ios" ]; then
    continue
  fi
  if [ "$arch" == "wasm" ]; then
    continue
  fi

  if [ "$os" == "android" ]; then
    prepare_android_toolchain "$arch" || {
      echo "os="$os" arch="$arch" toolchain setup fail"
      exit 1
    }
    CGO_ENABLED=1 GOOS=$os GOARCH=$arch CC="$ANDROID_CC" CXX="$ANDROID_CXX" go build -ldflags="-s -w"
  else
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w"
  fi

  if [ $? -ne 0 ]; then
    echo "os="$os" arch="$arch" build fail"
    exit 1
  fi
  if [ "$os" = "windows" ]; then
    mv cmd.exe ${NAME}.exe
    zip ${NAME}_"${os}"_"${arch}"".zip" ${NAME}.exe
    if [ $? -ne 0 ]; then
      echo "os="$os" arch="$arch" zip fail"
      exit 1
    fi
    mv ${NAME}_"${os}"_"${arch}"".zip" pack/
    rm $NAME".exe" -f
  else
    mv cmd ${NAME}
    zip ${NAME}_"${os}"_"${arch}"".zip" ${NAME}
    if [ $? -ne 0 ]; then
      echo "os="$os" arch="$arch" zip fail"
      exit 1
    fi
    mv ${NAME}_"${os}"_"${arch}"".zip" pack/
    rm $NAME -f
  fi
  echo "os="$os" arch="$arch" done build"
done

zip pack.zip pack/ -r

echo "all done"
