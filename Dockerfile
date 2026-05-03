FROM node:24-alpine AS frontend

WORKDIR /app

COPY package.json package-lock.json tsconfig.json vite.config.ts postcss.config.js tailwind.config.js ./
COPY web ./web
RUN npm ci && npm run build

FROM golang:1.25-alpine AS builder

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /app/web/dist ./web/dist

RUN go build -ldflags="-w -s" -o /go/bin/server .

FROM alpine:latest

RUN apk add --no-cache ca-certificates ffmpeg tzdata

WORKDIR /app

ENV CLOUDCONV_ADDR=:3000
ENV CLOUDCONV_DB_PATH=/app/data/cloudconv.db
ENV CLOUDCONV_UPLOAD_DIR=/app/uploads
ENV CLOUDCONV_CONVERTED_DIR=/app/converted

COPY --from=builder /go/bin/server .
COPY --from=frontend /app/web/dist ./web/dist

RUN mkdir -p /app/data /app/uploads /app/converted

VOLUME ["/app/data", "/app/uploads", "/app/converted"]

EXPOSE 3000

CMD ["./server"]
