FROM golang:1.16.4-alpine AS builder

RUN apk update && apk upgrade && \
    apk add --no-cache bash git openssh ca-certificates

COPY . /app
WORKDIR /app

ENV GO111MODULE=on
RUN go mod download

RUN CGO_ENABLED=0 go build -o /hue-exporter .

FROM alpine

RUN apk update && apk upgrade && \
    apk add --no-cache ca-certificates

COPY --from=builder /hue-exporter /hue-exporter
