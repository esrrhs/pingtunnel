#! /bin/bash
set -x

CGO_ENABLED=0 go build
zip pingtunnel_linux64.zip pingtunnel

GOOS=darwin GOARCH=amd64 go build
zip pingtunnel_mac.zip pingtunnel

GOOS=windows GOARCH=amd64 go build
zip pingtunnel_windows64.zip pingtunnel.exe

GOOS=linux GOARCH=mipsle go build
zip pingtunnel_mipsle.zip pingtunnel

GOOS=linux GOARCH=arm go build
zip pingtunnel_arm.zip pingtunnel

GOOS=linux GOARCH=mips go build
zip pingtunnel_mips.zip pingtunnel

GOOS=windows GOARCH=386 go build
zip pingtunnel_windows32.zip pingtunnel.exe

GOOS=linux GOARCH=arm64 go build
zip pingtunnel_arm64.zip pingtunnel

GOOS=linux GOARCH=mips64 go build
zip pingtunnel_mips64.zip pingtunnel

GOOS=linux GOARCH=mips64le go build
zip pingtunnel_mips64le.zip pingtunnel

