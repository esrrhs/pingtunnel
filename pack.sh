#! /bin/bash
#set -x
NAME="pingtunnel"
rm *.zip -f

build_list="aix/ppc64
darwin/386
darwin/amd64
dragonfly/amd64
freebsd/386
freebsd/amd64
freebsd/arm
freebsd/arm64
illumos/amd64
linux/386
linux/amd64
linux/arm
linux/arm64
linux/mips
linux/mips64
linux/mipsle
linux/ppc64
linux/ppc64le
linux/riscv64
linux/s390x
netbsd/386
netbsd/amd64
netbsd/arm
netbsd/arm64
openbsd/386
openbsd/amd64
openbsd/arm
openbsd/arm64
plan9/386
plan9/amd64
plan9/arm
solaris/amd64
windows/386
windows/amd64
windows/arm"

for line in $build_list; do
  os=$(echo "$line" | awk -F"/" '{print $1}')
  arch=$(echo "$line" | awk -F"/" '{print $2}')
  echo "os="$os" arch="$arch" start build"
  if [ $os == "android" ]; then
    continue
  fi
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build
  if [ $? -ne 0 ]; then
    echo "os="$os" arch="$arch" build fail"
    exit 1
  fi
  zip ${NAME}_"${os}"_"${arch}"".zip" $NAME
  if [ $? -ne 0 ]; then
    echo "os="$os" arch="$arch" zip fail"
    exit 1
  fi
  echo "os="$os" arch="$arch" done build"
done

echo "all done"

