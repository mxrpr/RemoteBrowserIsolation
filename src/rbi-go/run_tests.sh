#!/bin/bash

set -e

# Change to the rbi-go directory
cd /home/mixer/projects/remote_browser_isolation/src/rbi-go

echo "=========================================="
echo "Step 1: Building all packages..."
echo "=========================================="
go build ./...
echo "✓ Build successful"
echo ""

echo "=========================================="
echo "Step 2: Running go vet..."
echo "=========================================="
go vet ./...
echo "✓ Vet passed"
echo ""

echo "=========================================="
echo "Step 3: Running all tests (verbose)..."
echo "=========================================="
go test ./... -v
echo "✓ Tests completed"
echo ""

echo "=========================================="
echo "All checks passed!"
echo "=========================================="
