# Stage 1: Build the Go binary
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies first to cache them
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build
COPY . .
RUN CGO_ENABLED=0 go build -o /app/gateway ./cmd/gateway

# Stage 2: Create a minimal runtime image
FROM alpine:3.19

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/gateway /app/gateway
# Create a dummy configs directory (can be overridden by volume mount)
RUN mkdir -p /app/configs

EXPOSE 8080

ENTRYPOINT ["./gateway"]
