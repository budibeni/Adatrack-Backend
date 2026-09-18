#!/bin/bash
export PATH=$PATH:/usr/local/go/bin

echo "Running go vet..."
go vet ./...

echo "Running tests with coverage..."
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -n 1
