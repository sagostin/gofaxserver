# Stage 1: Build the Go application
FROM golang:1.24.1-bookworm AS builder

# Configure Go proxy and set working directory
ENV GOPROXY=https://proxy.golang.org,direct
WORKDIR /app

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build the application
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./gofaxserver/cmd/gofaxserver

# Stage 2: Final image using Debian Bookworm Slim
FROM debian:bookworm-slim

# Update package lists and install runtime & build dependencies in one RUN command.
RUN apt-get update && apt-get install -y \
    ca-certificates \
    bash \
    ghostscript \
    curl \
    wget \
    autoconf \
    pkg-config \
    build-essential \
    libpng-dev && \
    rm -rf /var/lib/apt/lists/*

# Create a non-root user
RUN adduser --disabled-password --gecos "" appuser

# Download and build ImageMagick 7 from source
WORKDIR /tmp
RUN wget https://github.com/ImageMagick/ImageMagick/archive/refs/tags/7.1.0-31.tar.gz && \
    tar xzf 7.1.0-31.tar.gz && \
    cd ImageMagick-7.1.0-31 && \
    ./configure --prefix=/usr/local --with-bzlib=yes --with-fontconfig=yes --with-freetype=yes --with-gslib=yes --with-gvc=yes --with-jpeg=yes --with-jp2=yes --with-png=yes --with-tiff=yes --with-xml=yes --with-gs-font-dir=/usr/share/fonts --disable-static && \
    make -j$(nproc) && \
    make install && \
    ldconfig /usr/local/lib && \
    cd / && rm -rf /tmp/ImageMagick-7.1.0-31*

# Set working directory and copy the pre-built Go binary from the builder stage
WORKDIR /app
COPY --from=builder /app/main .

# Switch to non-root user
USER appuser

# Expose ports as needed
EXPOSE 8022
EXPOSE 8080

# Command to run the application
CMD ["./main"]