FROM golang AS build-env

WORKDIR /app

COPY go.* ./
RUN go mod download
COPY . ./
RUN go mod tidy
RUN cd cmd && go build -v -ldflags="-s -w -X 'github.com/esrrhs/pingtunnel.BuildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')' -X 'github.com/esrrhs/pingtunnel.GitBranch=docker'" -o pingtunnel && mv pingtunnel ../

FROM debian:bookworm-slim
COPY --from=build-env /app/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
