BINARY      := recall-server
SEED_BIN    := seed-export
PKG_SERVER  := ./cmd/server
PKG_SEED    := ./cmd/seed-export
TARGET_DIR  := target

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
BUILD_TAGS  := sqlite_fts5

# Cross C compiler for CGO (go-sqlite3). zig is the friction-free choice on macOS.
# Override with `make CC_CROSS=aarch64-linux-musl-gcc ...` if you prefer musl-cross.
ZIG         ?= zig

.PHONY: help all host run linux-amd64 linux-arm64 linux-armv7 linux-armv6 seed-export clean tidy

help:
	@echo "Targets:"
	@echo "  host          Build for the current machine into $(TARGET_DIR)/host/"
	@echo "  run           Build & run the server on the host (uses ./config.yaml)"
	@echo "  linux-amd64   Cross-build for x86_64 Linux         -> $(TARGET_DIR)/linux-amd64/"
	@echo "  linux-arm64   Cross-build for ARM64 Linux (Pi 3+)  -> $(TARGET_DIR)/linux-arm64/"
	@echo "  linux-armv7   Cross-build for ARMv7 Linux (Pi 2/3) -> $(TARGET_DIR)/linux-armv7/"
	@echo "  linux-armv6   Cross-build for ARMv6 Linux (Pi 0/1) -> $(TARGET_DIR)/linux-armv6/"
	@echo "  all           Build every target above"
	@echo "  seed-export   Build the seed-export tool for host"
	@echo "  clean         Remove $(TARGET_DIR)/"
	@echo ""
	@echo "Cross-compilation uses 'zig cc' for CGO. Install with: brew install zig"

all: linux-amd64 linux-arm64 linux-armv7 linux-armv6

host:
	@mkdir -p $(TARGET_DIR)/host
	CGO_ENABLED=1 go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/host/$(BINARY) $(PKG_SERVER)
	CGO_ENABLED=1 go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/host/$(SEED_BIN) $(PKG_SEED)

run: host
	$(TARGET_DIR)/host/$(BINARY) -config config.yaml

linux-amd64:
	@mkdir -p $(TARGET_DIR)/linux-amd64
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
	  CC="$(ZIG) cc -target x86_64-linux-musl" \
	  CXX="$(ZIG) c++ -target x86_64-linux-musl" \
	  go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/linux-amd64/$(BINARY) $(PKG_SERVER)

linux-arm64:
	@mkdir -p $(TARGET_DIR)/linux-arm64
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
	  CC="$(ZIG) cc -target aarch64-linux-musl" \
	  CXX="$(ZIG) c++ -target aarch64-linux-musl" \
	  go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/linux-arm64/$(BINARY) $(PKG_SERVER)

linux-armv7:
	@mkdir -p $(TARGET_DIR)/linux-armv7
	CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 \
	  CC="$(ZIG) cc -target arm-linux-musleabihf" \
	  CXX="$(ZIG) c++ -target arm-linux-musleabihf" \
	  go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/linux-armv7/$(BINARY) $(PKG_SERVER)

linux-armv6:
	@mkdir -p $(TARGET_DIR)/linux-armv6
	CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=6 \
	  CC="$(ZIG) cc -target arm-linux-musleabihf" \
	  CXX="$(ZIG) c++ -target arm-linux-musleabihf" \
	  go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/linux-armv6/$(BINARY) $(PKG_SERVER)

seed-export:
	@mkdir -p $(TARGET_DIR)/host
	CGO_ENABLED=1 go build -tags "$(BUILD_TAGS)" -ldflags="$(LDFLAGS)" -o $(TARGET_DIR)/host/$(SEED_BIN) $(PKG_SEED)

tidy:
	go mod tidy

clean:
	rm -rf $(TARGET_DIR)
