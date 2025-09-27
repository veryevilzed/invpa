# Makefile for invpa project

# Variables
APP_NAME_CLI := invpa
APP_NAME_REPORTER := reporter
CMD_PATH_CLI := ./cli
CMD_PATH_REPORTER := ./cmd/reporter
BUILD_DIR := ./build

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

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@rm -f $(APP_NAME_CLI) $(APP_NAME_REPORTER)
	@rm -rf temp
	@echo "Clean complete."

# Run the web server
run-web:
	@echo "Starting web server on http://localhost:8080"
	@go run cmd/web/main.go

.PHONY: all build build-all build-windows build-mac clean run-web
