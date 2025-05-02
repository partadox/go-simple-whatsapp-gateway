FROM golang:1.20-alpine AS builder

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY . .
RUN go mod download

# Build with CGO enabled
RUN CGO_ENABLED=1 go build -o app

FROM alpine:latest

# Install runtime dependencies for SQLite
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