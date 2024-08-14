FROM golang AS build-env

WORKDIR /app

COPY go.* ./
RUN go mod download
COPY . ./
RUN go mod tidy
RUN cd cmd && go build -v -o pingtunnel && mv pingtunnel ../

FROM debian
COPY --from=build-env /app/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
