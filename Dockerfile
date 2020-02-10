FROM golang AS build-env

RUN go get -u github.com/esrrhs/pingtunnel
RUN go get -u github.com/esrrhs/pingtunnel/...
RUN go install github.com/esrrhs/pingtunnel

FROM debian
COPY --from=build-env /go/bin/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
