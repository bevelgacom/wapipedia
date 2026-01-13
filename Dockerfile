FROM golang:1.24-alpine as build

RUN apk add --no-cache gcc musl-dev

ENV CGO_ENABLED=0

COPY ./ /go/src/github.com/bevelgacom/wapipedia

WORKDIR /go/src/github.com/bevelgacom/wapipedia

RUN go build -o wapipedia ./cmd/wapipedia

FROM alpine:edge

RUN apk add --no-cache ca-certificates tzdata

RUN mkdir /opt/wapipedia
WORKDIR /opt/wapipedia

# Create data directory for ZIM files
RUN mkdir /data

COPY --from=build /go/src/github.com/bevelgacom/wapipedia/wapipedia /usr/local/bin
COPY --from=build /go/src/github.com/bevelgacom/wapipedia/static /opt/wapipedia/static

# Default ZIM path
ENV WAPIPEDIA_ZIM=/data/wikipedia.zim

EXPOSE 8080

ENTRYPOINT [ "wapipedia", "serve" ]

