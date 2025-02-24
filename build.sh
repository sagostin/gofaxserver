#!/bin/bash

# Script to build the Docker image

# Variables
IMAGE_NAME="gofaxserver"
IMAGE_TAG="latest"
DOCKERFILE="Dockerfile"
BUILD_CONTEXT="."

# Check for Docker
if ! command -v docker &> /dev/null; then
    echo "Docker is not installed or not in the PATH. Please install Docker and try again."
    exit 1
fi

# Build the Docker image
echo "Building the Docker image..."
docker build -t "${IMAGE_NAME}:${IMAGE_TAG}" -f "${DOCKERFILE}" "${BUILD_CONTEXT}"

# Check if the build was successful
if [ $? -eq 0 ]; then
    echo "Docker image built successfully!"
    echo "Image name: ${IMAGE_NAME}"
    echo "Image tag: ${IMAGE_TAG}"
else
    echo "Docker image build failed."
    exit 1
fi