#!/bin/bash

set -e

# Build script for KubeCostGuard

APP_NAME="kubecostguard"
VERSION=${VERSION:-latest}
REGISTRY=${REGISTRY:-kubecostguard}

echo "Building ${APP_NAME}:${VERSION}"

# Build binary
echo "Building Go binary..."
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/${APP_NAME} cmd/kubecostguard/main.go

# Build Docker image
echo "Building Docker image..."
docker build -t ${REGISTRY}/${APP_NAME}:${VERSION} .

echo "Build completed successfully!"
echo "Image: ${REGISTRY}/${APP_NAME}:${VERSION}"