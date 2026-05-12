APP_NAME := nixpkgs-notifier
BIN_DIR := bin

LINUX_BINARY := $(BIN_DIR)/$(APP_NAME)-linux-amd64

.PHONY: all build generate run test clean

all: build

generate:
	go run github.com/a-h/templ/cmd/templ@v0.3.977 generate

build: generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(LINUX_BINARY) ./cmd/server
	chmod +x $(LINUX_BINARY)

run: generate
	go run ./cmd/server

test:
	go test -v ./...

clean:
	rm -rf $(BIN_DIR)