# Stage 1: build
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# GOAMD64=v2 (SSE4.2/POPCNT baseline, any x86-64 CPU from ~2009+) lets the Go
# compiler emit faster instruction sequences; -s -w strips debug info from the
# image. The SQLite C core is compiled by gcc with its own -O2 regardless.
RUN CGO_ENABLED=1 GOAMD64=v2 go build -tags "fts5" -ldflags="-s -w" -o bot .

# Stage 2: runtime
FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite

WORKDIR /app

COPY --from=builder /app/bot .

VOLUME ["/app/storage"]

CMD ["./bot"]
