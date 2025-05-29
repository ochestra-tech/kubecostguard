#!/bin/bash

set -e

# Test script for KubeCostGuard

echo "Running tests for KubeCostGuard..."

# Run unit tests
echo "Running unit tests..."
go test ./... -v

# Run tests with coverage
echo "Running tests with coverage..."
go test -coverprofile=coverage.out ./...

# Generate coverage report
echo "Generating coverage report..."
go tool cover -html=coverage.out -o coverage.html

echo "Coverage report generated: coverage.html"

# Run linting (if golangci-lint is available)
if command -v golangci-lint &> /dev/null; then
    echo "Running linter..."
    golangci-lint run
else
    echo "golangci-lint not found, skipping linting"
fi

echo "All tests completed!"