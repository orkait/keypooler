# Build stage - pure Go, CGO-free (no C toolchain or SQLite headers needed).
# Go 1.25 matches the go.mod directive (modernc.org/sqlite requires >= 1.25).
FROM golang:1.25-bookworm AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully static binary.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/keypooler ./cmd/keypooler \
    && mkdir -p /app/data

# Runtime stage - distroless static: ca-certificates + nonroot, no shell or
# package manager. The binary is static (modernc.org/sqlite + libsql are pure Go).
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /app/keypooler .
COPY --from=builder /app/migrations ./migrations
# Writable data dir for the optional local SQLite path (prod uses Turso/libSQL).
COPY --from=builder --chown=nonroot:nonroot /app/data ./data

EXPOSE 8080

ENTRYPOINT ["/app/keypooler"]
