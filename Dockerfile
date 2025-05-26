# File: Dockerfile
# Build stage
FROM golang:1.21-alpine AS builder

# Install necessary build tools
RUN apk --no-cache add git make

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o kubecostguard ./cmd/kubecostguard

# Final stage
FROM alpine:3.18

# Install necessary runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' appuser

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/kubecostguard /app/kubecostguard

# Change ownership
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Command to run
CMD ["/app/kubecostguard"]