#! /bin/bash
#set -x
NAME="pingtunnel"
rm *.zip -f

#go tool dist list
build_list="aix/ppc64
android/386
android/amd64
android/arm
android/arm64
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
linux/mips64le
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

rm pack -rf
rm pack.zip -f
mkdir pack

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
  if [ $os = "windows" ]; then
    zip ${NAME}_"${os}"_"${arch}"".zip" $NAME".exe"
    if [ $? -ne 0 ]; then
      echo "os="$os" arch="$arch" zip fail"
      exit 1
    fi
    mv ${NAME}_"${os}"_"${arch}"".zip" pack/
    rm $NAME".exe" -f
  else
    zip ${NAME}_"${os}"_"${arch}"".zip" $NAME
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


