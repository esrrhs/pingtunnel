FROM golang AS build-env

WORKDIR /app

COPY * ./
RUN ls -a
RUN go mod download
COPY . ./
RUN go mod tidy
RUN cd cmd
RUN go build -v -o pingtunnel
RUN cd ..

FROM debian
COPY --from=build-env /app/cmd/pingtunnel .
COPY GeoLite2-Country.mmdb .
WORKDIR ./
