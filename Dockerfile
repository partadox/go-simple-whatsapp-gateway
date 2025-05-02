# Stage 1: Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go.mod dan go.sum terlebih dahulu
COPY go.mod go.sum ./

# Edit go.mod untuk kompatibilitas
RUN sed -i 's/go 1.24.2/go 1.21/' go.mod && \
    sed -i 's/go 1.23.0/go 1.21/' go.mod && \
    sed -i '/toolchain/d' go.mod

# Download dependencies
RUN go mod download

# Copy seluruh kode sumber
COPY . .

# Build dengan CGO_ENABLED=1
RUN CGO_ENABLED=1 go build -o app

# Stage 2: Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata sqlite

# Siapkan direktori aplikasi
WORKDIR /app

# Copy binary dan file-file yang diperlukan
COPY --from=builder /app/app .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static
COPY --from=builder /app/.env ./.env

# Buat direktori data WhatsApp
RUN mkdir -p /app/whatsapp-data

# Set izin yang benar
RUN chmod +x /app/app

# Expose port aplikasi
EXPOSE 8080

# Set environment variables
ENV GIN_MODE=release
ENV LISTEN_ADDR=:8080
ENV WHATSAPP_DATA_DIR=/app/whatsapp-data

# Jalankan aplikasi
CMD ["./app"]