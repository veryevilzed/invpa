# Makefile for invpa project

# Variables
APP_NAME_CLI := invpa
APP_NAME_REPORTER := reporter
APP_NAME_WEB := invpa-web
CMD_PATH_CLI := ./cli
CMD_PATH_REPORTER := ./cmd/reporter
CMD_PATH_WEB := ./cmd/web
BUILD_DIR := ./build
ARGS :=

# Default target
all: build

# Build for the current OS
build:
	@echo "Building for current OS..."
	go build -o $(APP_NAME_CLI) $(CMD_PATH_CLI)
	go build -o $(APP_NAME_REPORTER) $(CMD_PATH_REPORTER)
	@echo "Build complete. Executables are in the root directory."

# Build for all platforms
build-all: build-windows build-mac

# Build for Windows
build-windows:
	@echo "Building for Windows..."
	@mkdir -p $(BUILD_DIR)/windows
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/windows/$(APP_NAME_REPORTER).exe $(CMD_PATH_REPORTER)
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/windows/$(APP_NAME_CLI).exe $(CMD_PATH_CLI)
	@echo "Windows executables are in $(BUILD_DIR)/windows/"

# Build for macOS (Intel)
build-mac:
	@echo "Building for macOS (amd64)..."
	@mkdir -p $(BUILD_DIR)/mac
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/mac/$(APP_NAME_REPORTER) $(CMD_PATH_REPORTER)
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/mac/$(APP_NAME_CLI) $(CMD_PATH_CLI)
	@echo "macOS executables are in $(BUILD_DIR)/mac/"

# Build web server for Linux (using Zig for CGO cross-compilation)
build-web:
	@echo "Building web server for Linux (x86_64-linux-gnu)..."
	@mkdir -p $(BUILD_DIR)/linux
	export CC="zig cc -target x86_64-linux-gnu"; \
	export CXX="zig c++ -target x86_64-linux-gnu"; \
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/linux/$(APP_NAME_WEB) $(CMD_PATH_WEB)
	@echo "Linux web server executable is in $(BUILD_DIR)/linux/"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f $(APP_NAME_CLI) $(APP_NAME_REPORTER)
	@rm -rf temp
	@echo "Clean complete."

# Run the web server
run-web:
	@echo "Starting web server..."
	@go run cmd/web/main.go $(ARGS)

.PHONY: all build build-all build-windows build-mac build-web clean run-web
