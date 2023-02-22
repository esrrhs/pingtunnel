FROM golang AS build-env

RUN GO111MODULE=off go get -u github.com/esrrhs/pingtunnel
RUN GO111MODULE=off go get -u github.com/esrrhs/pingtunnel/...
RUN GO111MODULE=off go install github.com/esrrhs/pingtunnel

FROM debian
COPY --from=build-env /go/bin/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
