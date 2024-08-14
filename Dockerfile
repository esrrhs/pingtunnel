FROM golang AS build-env

WORKDIR /app

COPY cmd/go.* ./
RUN ls -a
RUN go mod download
COPY . ./
RUN go mod tidy
RUN go build -v -o pingtunnel

FROM debian
COPY --from=build-env /app/cmd/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
