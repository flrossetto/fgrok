PATH := $(PATH):$(shell go env GOPATH)/bin

.PHONY: build run clean deps proto

# Generate gRPC code
proto:
	protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. internal/proto/fgrok.proto

# Build the application
build: proto
	go build -o bin/fgrok cmd/fgrok/main.go

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.3.0 run ./...

# Run with hot reload using reflex
run.server:
	reflex -r '\.go$$' -s -- go run ./cmd/fgrok server
run.client:
	reflex -r '\.go$$' -s -- go run ./cmd/fgrok client

# Install dependencies
deps:
	go mod tidy
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go get github.com/spf13/cobra@latest
	go get github.com/cespare/reflex@latest

# Clean build artifacts
clean:
	rm -rf bin/
