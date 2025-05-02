FROM golang:1.20-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
# RUN go build -o app
# Build with CGO enabled
RUN CGO_ENABLED=1 go build -o app

FROM alpine:latest

# Create app directory
WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates tzdata

# Copy binary and necessary files from builder
COPY --from=builder /app/app .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# Create data directory
RUN mkdir -p /app/whatsapp-data

# Expose port
EXPOSE 8080

# Set environment variable
ENV GIN_MODE=release
ENV CGO_ENABLED=1

# Run the application
CMD ["./app"]