APP_NAME ?= ollama-chat-client
CMD ?= ./cmd/server
DIST_DIR ?= dist
BIN_DIR ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w
GO_CACHE_DIR ?= $(CURDIR)/.gocache

.DEFAULT_GOAL := all

.PHONY: all build build-mac build-linux build-windows release checksums clean \
	build-darwin-amd64 build-darwin-arm64 \
	build-linux-amd64 build-linux-arm64 \
	build-windows-amd64 build-windows-arm64

all: release

build:
	mkdir -p $(BIN_DIR)
	GOCACHE=$(GO_CACHE_DIR) CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) $(CMD)

build-mac: build-darwin-amd64 build-darwin-arm64

build-linux: build-linux-amd64 build-linux-arm64

build-windows: build-windows-amd64 build-windows-arm64

release: clean build-mac build-linux build-windows checksums

build-darwin-amd64:
	$(call package_binary,darwin,amd64)

build-darwin-arm64:
	$(call package_binary,darwin,arm64)

build-linux-amd64:
	$(call package_binary,linux,amd64)

build-linux-arm64:
	$(call package_binary,linux,arm64)

build-windows-amd64:
	$(call package_binary,windows,amd64)

build-windows-arm64:
	$(call package_binary,windows,arm64)

checksums:
	cd $(DIST_DIR) && shasum -a 256 *.tar.gz *.zip > checksums.txt

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

define package_binary
	mkdir -p $(DIST_DIR)
	os="$(1)"; \
	arch="$(2)"; \
	ext=""; \
	if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	name="$(APP_NAME)_$(VERSION)_$${os}_$${arch}"; \
	work="$(DIST_DIR)/$${name}"; \
	mkdir -p "$$work"; \
	echo "building $$name"; \
	GOCACHE=$(GO_CACHE_DIR) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o "$$work/$(APP_NAME)$${ext}" $(CMD); \
	cp README.md "$$work/"; \
	cp .env.example "$$work/"; \
	if [ "$$os" = "windows" ]; then \
		(cd $(DIST_DIR) && zip -qr "$$name.zip" "$$name"); \
	else \
		(cd $(DIST_DIR) && tar -czf "$$name.tar.gz" "$$name"); \
	fi; \
	rm -rf "$$work"
endef
