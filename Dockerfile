FROM golang:1.24-alpine AS build

RUN apk add --no-cache imagemagick-dev gcc musl-dev pkgconfig imagemagick imagemagick-webp imagemagick-tiff imagemagick-svg imagemagick-jpeg imagemagick-heic

ENV CGO_ENABLED=1

COPY ./ /go/src/github.com/bevelgacom/wapipedia

WORKDIR /go/src/github.com/bevelgacom/wapipedia

RUN go build -o wapipedia ./cmd/wapipedia

FROM alpine:edge

RUN apk add --no-cache ca-certificates tzdata imagemagick-dev imagemagick imagemagick-webp imagemagick-tiff imagemagick-svg imagemagick-jpeg imagemagick-heic

RUN mkdir /opt/wapipedia
WORKDIR /opt/wapipedia

# Create data directory for ZIM files
RUN mkdir /data

COPY --from=build /go/src/github.com/bevelgacom/wapipedia/wapipedia /usr/local/bin
COPY /static /opt/wapipedia/static

# Default ZIM path
ENV WAPIPEDIA_ZIM=/data/wikipedia.zim

EXPOSE 8080

ENTRYPOINT [ "wapipedia", "serve" ]

