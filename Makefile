APP_NAME ?= chattone
APP_DISPLAY_NAME ?= ChatTone
APP_BUNDLE_ID ?= it.billnice.chattone
CMD ?= ./cmd/server
DIST_DIR ?= dist
BIN_DIR ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w
GO_CACHE_DIR ?= $(CURDIR)/.gocache
ICON_ICNS ?= assets/logo.icns
ICON_ICO ?= assets/logo.ico

.DEFAULT_GOAL := all

.PHONY: all icons build build-mac build-linux build-windows release checksums clean \
	build-darwin-amd64 build-darwin-arm64 \
	build-linux-amd64 build-linux-arm64 \
	build-windows-amd64 build-windows-arm64

all: release

icons:
	@echo "generating icons"
	@GOCACHE=$(GO_CACHE_DIR) go run ./tools/icongen

build:
	@echo "building $(BIN_DIR)/$(APP_NAME)"
	@mkdir -p $(BIN_DIR)
	@err="$(BIN_DIR)/go-build.err"; \
	GOCACHE=$(GO_CACHE_DIR) CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) $(CMD) 2> "$$err"; \
	status=$$?; \
	if [ $$status -ne 0 ]; then cat "$$err" >&2; rm -f "$$err"; exit $$status; fi; \
	grep -v '^go: writing stat cache:' "$$err" >&2 || true; \
	rm -f "$$err"

build-mac: icons build-darwin-amd64 build-darwin-arm64

build-linux: build-linux-amd64 build-linux-arm64

build-windows: icons build-windows-amd64 build-windows-arm64

release: icons clean build-mac build-linux build-windows checksums

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
	@echo "writing checksums"
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz *.zip > checksums.txt

clean:
	@echo "cleaning build outputs"
	@rm -rf $(BIN_DIR) $(DIST_DIR)

define package_binary
	@mkdir -p $(DIST_DIR)
	@os="$(1)"; \
	arch="$(2)"; \
	ext=""; \
	if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	name="$(APP_NAME)_$(VERSION)_$${os}_$${arch}"; \
	work="$(DIST_DIR)/$${name}"; \
	mkdir -p "$$work"; \
	echo "building release asset $$name"; \
	err="$$work/go-build.err"; \
	GOCACHE=$(GO_CACHE_DIR) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o "$$work/$(APP_NAME)$${ext}" $(CMD) 2> "$$err"; \
	status=$$?; \
	if [ $$status -ne 0 ]; then cat "$$err" >&2; rm -f "$$err"; exit $$status; fi; \
	grep -v '^go: writing stat cache:' "$$err" >&2 || true; \
	rm -f "$$err"; \
	cp README.md "$$work/"; \
	cp .env.example "$$work/"; \
	if [ "$$os" = "darwin" ]; then \
		app="$$work/$(APP_DISPLAY_NAME).app"; \
		mkdir -p "$$app/Contents/MacOS" "$$app/Contents/Resources"; \
		cp "$$work/$(APP_NAME)" "$$app/Contents/Resources/$(APP_NAME)"; \
		cp "$(ICON_ICNS)" "$$app/Contents/Resources/AppIcon.icns"; \
		printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' '<plist version="1.0">' '<dict>' '  <key>CFBundleExecutable</key>' '  <string>$(APP_NAME)</string>' '  <key>CFBundleIconFile</key>' '  <string>AppIcon</string>' '  <key>CFBundleIdentifier</key>' '  <string>$(APP_BUNDLE_ID)</string>' '  <key>CFBundleName</key>' '  <string>$(APP_DISPLAY_NAME)</string>' '  <key>CFBundleDisplayName</key>' '  <string>$(APP_DISPLAY_NAME)</string>' '  <key>CFBundlePackageType</key>' '  <string>APPL</string>' '  <key>CFBundleShortVersionString</key>' '  <string>$(VERSION)</string>' '  <key>CFBundleVersion</key>' '  <string>$(VERSION)</string>' '  <key>LSBackgroundOnly</key>' '  <true/>' '</dict>' '</plist>' > "$$app/Contents/Info.plist"; \
		printf '%s\n' '#!/bin/sh' 'LOG_DIR="$${HOME}/Library/Logs/$(APP_DISPLAY_NAME)"' 'mkdir -p "$$LOG_DIR"' 'cd "$$(dirname "$$0")/../Resources"' 'export OPEN_BROWSER="$${OPEN_BROWSER:-true}"' 'exec "./$(APP_NAME)" >> "$$LOG_DIR/$(APP_NAME).log" 2>&1' > "$$app/Contents/MacOS/$(APP_NAME)"; \
		chmod +x "$$app/Contents/MacOS/$(APP_NAME)"; \
	fi; \
	if [ "$$os" = "windows" ]; then \
		cp "$(ICON_ICO)" "$$work/logo.ico"; \
		printf '%s\r\n' '@echo off' 'set "LOG_DIR=%LOCALAPPDATA%\$(APP_DISPLAY_NAME)\Logs"' 'if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"' 'if not defined OPEN_BROWSER set OPEN_BROWSER=true' '$(APP_NAME).exe >> "%LOG_DIR%\$(APP_NAME).log" 2>&1' > "$$work/run-with-logs.cmd"; \
	fi; \
	if [ "$$os" = "windows" ]; then \
		(cd $(DIST_DIR) && zip -qr "$$name.zip" "$$name"); \
	else \
		(cd $(DIST_DIR) && tar -czf "$$name.tar.gz" "$$name"); \
	fi; \
	rm -rf "$$work"
endef
