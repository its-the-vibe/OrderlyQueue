# Build stage
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o orderlyq .

# Final stage (distroless)
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /

# Copy binary from builder
COPY --from=builder /app/orderlyq /orderlyq

USER nonroot:nonroot

ENTRYPOINT ["/orderlyq"]
