# Build stage
FROM golang:1.24.3-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o orderly-queue .

# Final stage
FROM scratch

WORKDIR /

# Copy binary from builder
COPY --from=builder /app/orderly-queue /orderly-queue

# Copy default config (actual config should be mounted)
COPY --from=builder /app/config/config.example.yaml /config/config.example.yaml

ENTRYPOINT ["/orderly-queue"]
