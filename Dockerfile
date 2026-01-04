FROM golang:alpine AS builder

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

RUN apk add --no-cache git build-base

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -ldflags="-w -s" -o /go/bin/server .

FROM alpine:latest

RUN apk add --no-cache ffmpeg

WORKDIR /app

COPY --from=builder /go/bin/server .

COPY templates ./templates

RUN mkdir -p /app/uploads /app/converted

EXPOSE 3000

CMD ["./server"]
