#!/bin/bash

set -e

# Navigate to backend directory
cd "$(dirname "$0")/.."

echo "Running golangci-lint..."

# Check if golangci-lint is installed
if ! command -v golangci-lint &> /dev/null; then
    echo "golangci-lint is not installed."
    echo "Please install it using one of these methods:"
    echo "  - brew install golangci-lint"
    echo "  - Or download from: https://github.com/golangci/golangci-lint/releases"
    echo ""
    exit 1
fi

# Run golangci-lint
golangci-lint run --config=.golangci.yml

echo "Linting completed successfully!"
