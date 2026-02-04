FROM golang:1.24-bookworm AS builder
WORKDIR /go/src/app

# Install build dependencies for CGO (SQLite)
RUN apt-get update && apt-get install -y gcc libc6-dev && rm -rf /var/lib/apt/lists/*

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=1 go build -o ./bin/blossom ./cmd/api/main.go

# Runtime image - need glibc for CGO
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

COPY --from=builder /go/src/app/bin/blossom /usr/local/bin/blossom
COPY --from=builder /go/src/app/db/migrations /app/db/migrations

# Create data directories
RUN mkdir -p /data/blobs /data/db

WORKDIR /app
EXPOSE 8000/tcp
CMD ["/usr/local/bin/blossom"]
