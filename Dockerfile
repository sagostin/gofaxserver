# Start from a Golang base image
FROM golang:1.22-alpine AS builder

# Configure Go proxy
ENV GOPROXY=https://proxy.golang.org,direct

# Set the working directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./gofaxserver/cmd/gofaxserver

# Start a new stage from scratch
FROM alpine:latest

# Add CA certificates and ffmpeg, then create a non-root user
RUN apk --no-cache add ca-certificates ffmpeg curl && \
    adduser -D appuser

# Set the working directory
WORKDIR /app

# Copy the pre-built binary file from the previous stage
COPY --from=builder /app/main .

# Use the non-root user
USER appuser

# Expose port 3000
EXPOSE 8022
EXPOSE 8080

# Command to run the executable
CMD ["./main"]