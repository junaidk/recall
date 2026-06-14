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

.PHONY: help all host run linux-amd64 linux-arm64 linux-armv7 linux-armv6 seed-export seed-conjugations seed-noun-plurals clean tidy deploy deploy-service service-status service-logs

# ---- Deployment (override on the command line, e.g. make deploy REMOTE=me@host ARCH=linux-arm64) ----
# NOTE: no inline comments on these — make keeps trailing spaces before '#' as
# part of the value, which would corrupt the paths/unit name built from them.
REMOTE      ?= root@192.168.88.119
REMOTE_DIR  ?= /opt/recall
SERVICE     ?= recall
SVC_USER    ?= recall
ARCH        ?= linux-amd64

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
	@echo "  seed-conjugations  Build seed/de_verb_conjugations.jsonl from kaikki German extract"
	@echo "  seed-noun-plurals  Build seed/de_noun_plurals.jsonl from kaikki German extract"
	@echo "  clean         Remove $(TARGET_DIR)/"
	@echo ""
	@echo "Deployment (override REMOTE/REMOTE_DIR/ARCH on the command line):"
	@echo "  deploy-service One-time host setup: service user, dirs, systemd unit, config"
	@echo "  deploy         Build (ARCH=$(ARCH)), rsync binary + seed, restart $(SERVICE)"
	@echo "  service-status Show systemctl status of $(SERVICE) on $(REMOTE)"
	@echo "  service-logs   Follow journald logs for $(SERVICE) on $(REMOTE)"
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

# Fetch the kaikki German Wiktionary extract and emit seed/de_verb_conjugations.jsonl.
# Run once; the produced file is committed and consumed at boot by internal/conjugations.
# Override -src to point at a local copy of the kaikki .jsonl.gz if you've already downloaded it.
seed-conjugations:
	go run ./cmd/build-conjugations -out seed/de_verb_conjugations.jsonl

# Fetch the kaikki German Wiktionary extract and emit seed/de_noun_plurals.jsonl.
# Run once; the produced file is committed and consumed at boot by internal/plurals.
# Override -src to point at a local copy of the kaikki .jsonl.gz if you've already downloaded it.
seed-noun-plurals:
	go run ./cmd/build-noun-plurals -out seed/de_noun_plurals.jsonl

# ---- Deployment ----------------------------------------------------------
# The deployable runtime is just the Go binary + the seed corpus + a writable
# data dir for the SQLite DB and audio cache. The frontend and DB schema are
# embedded in the binary (internal/web, internal/db), so nothing else ships.
# config.yaml holds secrets and is host-managed: installed once from the
# example by deploy-service, then never overwritten by deploy.

# One-time setup on the remote host: dedicated service user, install root the
# deploy user can write to, the systemd unit (paths rewritten to REMOTE_DIR),
# and a starter config the operator edits in place.
deploy-service:
	ssh $(REMOTE) 'SUDO=$$(command -v sudo||true); id $(SVC_USER) >/dev/null 2>&1 || $$SUDO useradd --system --home $(REMOTE_DIR) --shell /usr/sbin/nologin $(SVC_USER)'
	ssh $(REMOTE) 'SUDO=$$(command -v sudo||true); $$SUDO mkdir -p $(REMOTE_DIR)/data $(REMOTE_DIR)/seed && $$SUDO chown -R $$(whoami) $(REMOTE_DIR) && $$SUDO chown -R $(SVC_USER):$(SVC_USER) $(REMOTE_DIR)/data'
	sed 's#/opt/recall#$(REMOTE_DIR)#g' deploy/recall.service \
		| ssh $(REMOTE) 'SUDO=$$(command -v sudo||true); $$SUDO tee /etc/systemd/system/$(SERVICE).service >/dev/null'
	ssh $(REMOTE) 'test -f $(REMOTE_DIR)/config.yaml' || \
		(rsync -avz config.example.yaml $(REMOTE):$(REMOTE_DIR)/config.yaml && \
		 echo "==> installed starter config at $(REMOTE_DIR)/config.yaml — edit session_secret + deepl.api_key on the host")
	ssh $(REMOTE) 'SUDO=$$(command -v sudo||true); $$SUDO systemctl daemon-reload && $$SUDO systemctl enable $(SERVICE)'
	@echo "==> systemd unit $(SERVICE) installed; edit $(REMOTE_DIR)/config.yaml, then run 'make deploy'"

# Build for the target arch, push the binary + seed corpus, then restart.
deploy: $(ARCH)
	rsync -avz $(TARGET_DIR)/$(ARCH)/$(BINARY) $(REMOTE):$(REMOTE_DIR)/$(BINARY)
	rsync -avz --delete seed/                  $(REMOTE):$(REMOTE_DIR)/seed/
	ssh $(REMOTE) 'SUDO=$$(command -v sudo||true); $$SUDO systemctl restart $(SERVICE)'
	@echo "==> deployed $(ARCH) build and restarted $(SERVICE) on $(REMOTE)"

service-status:
	ssh $(REMOTE) 'systemctl status $(SERVICE) --no-pager'

service-logs:
	ssh -t $(REMOTE) 'journalctl -u $(SERVICE) -f'

tidy:
	go mod tidy

clean:
	rm -rf $(TARGET_DIR)
