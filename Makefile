BINARY=propq
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build clean install test lint run

build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-X 'github.com/v0id00/propq/internal/app.version=$(VERSION)'" \
		-o $(BUILD_DIR)/$(BINARY) ./cmd/propq/
	@echo "✓ Built $(BUILD_DIR)/$(BINARY)"

install:
	go install -ldflags="-X 'github.com/v0id00/propq/internal/app.version=$(VERSION)'" \
		./cmd/propq/
	@echo "✓ Installed $(BINARY) to $$GOPATH/bin/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR)
	@echo "✓ Cleaned"

test:
	go test ./...

lint:
	@which golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

run: build
	./$(BUILD_DIR)/$(BINARY) $(ARGS)

tidy:
	go mod tidy

update-deps:
	go get -u ./...
	go mod tidy
