# Build stage - pure Go, CGO-free. Go 1.25 matches the go.mod directive.
FROM golang:1.25-bookworm AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully static binary.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/keypooler ./cmd/keypooler

# Runtime stage - distroless static: ca-certificates + nonroot, no shell or
# package manager. The binary is static; all state lives in Postgres.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /app/keypooler .
COPY --from=builder /app/migrations ./migrations

EXPOSE 8080

ENTRYPOINT ["/app/keypooler"]
