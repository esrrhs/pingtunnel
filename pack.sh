#! /bin/bash
go build
zip pingtunnel_linux64.zip pingtunnel

GOOS=darwin GOARCH=amd64 go build
zip pingtunnel_mac.zip pingtunnel

GOOS=windows GOARCH=amd64 go build
zip pingtunnel_windows64.zip pingtunnel.exe

