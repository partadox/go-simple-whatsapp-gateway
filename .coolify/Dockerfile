FROM golang:1.21-alpine AS builder  
# Gunakan versi Go yang lebih baru

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY . .

# Edit go.mod untuk menghapus direktif baru
RUN sed -i 's/go 1.23.0/go 1.23/' go.mod && \
    sed -i '/toolchain/d' go.mod

RUN go mod download
RUN CGO_ENABLED=1 go build -o app

FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata sqlite

# Create app directory
WORKDIR /app

# Copy binary and necessary files from builder
COPY --from=builder /app/app .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static
COPY --from=builder /app/.env ./.env

# Create data directory
RUN mkdir -p /app/whatsapp-data

# Expose port
EXPOSE 8080

# Set environment variable
ENV GIN_MODE=release

# Run the application
CMD ["./app"]