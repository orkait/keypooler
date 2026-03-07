# Build stage
FROM golang:1.22-bookworm AS builder

WORKDIR /app

# Install SQLite dev headers for CGO
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=1 go build -o /app/key-pool-system ./cmd/

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates libsqlite3-0 python3 nodejs \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy binary and migrations
COPY --from=builder /app/key-pool-system .
COPY --from=builder /app/migrations ./migrations

# Create data directory for SQLite
RUN mkdir -p /app/data

EXPOSE 8080

ENTRYPOINT ["./key-pool-system"]
