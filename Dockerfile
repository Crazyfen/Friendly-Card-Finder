# Stage 1: build
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -tags "fts5" -o bot ./...

# Stage 2: runtime
FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite

WORKDIR /app

COPY --from=builder /app/bot .

VOLUME ["/app/storage"]

CMD ["./bot"]
