.PHONY: help generate build run-server run-client test clean buf-push

help: ## Display this help screen
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

generate: ## Generate code from proto files
	buf generate

build: ## Build server and client binaries
	go build -o bin/server cmd/server/main.go
	go build -o bin/client cmd/client/main.go

run-server: ## Run the gRPC server
	go run cmd/server/main.go

run-client: ## Run the sample client
	go run cmd/client/main.go

test: ## Run tests
	go test -v ./...

clean: ## Clean generated files and binaries
	rm -rf gen/
	rm -rf bin/

buf-lint: ## Lint proto files
	buf lint

buf-format: ## Format proto files
	buf format -w

buf-push: ## Push to Buf Schema Registry
	buf push

buf-login: ## Login to Buf Schema Registry
	buf registry login

install-deps: ## Install Go dependencies
	go mod download
	go mod tidy

install-buf: ## Install Buf CLI
	go install github.com/bufbuild/buf/cmd/buf@latest

deps: install-buf install-deps ## Install all dependencies

all: generate build ## Generate code and build binaries
