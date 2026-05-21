# Deploying Recall

Recall ships as a single Go binary. Two deployment paths are documented here:

- **Docker / Docker Compose** — one command, works anywhere with a container runtime.
- **systemd** — for installing the binary directly on a Linux host (server, VPS, Raspberry Pi, etc.).

Pick whichever fits your host.

## Option A — Docker Compose

Prereqs: Docker 20.10+ with the Compose plugin.

```bash
cp config.example.yaml config.yaml
# Edit config.yaml — set server.session_secret, deepl.api_key, etc.

docker compose -f deploy/docker-compose.yml up -d --build
```

That builds the image from source (multi-stage; CGO + `sqlite_fts5`), persists the SQLite DB and audio cache to `./docker-data/` on the host, and bind-mounts `config.yaml` read-only.

```bash
docker compose -f deploy/docker-compose.yml logs -f       # follow logs
docker compose -f deploy/docker-compose.yml restart       # restart
docker compose -f deploy/docker-compose.yml down          # stop
```

To upgrade, pull the new code and re-run `up -d --build`.

## Option B — systemd (any Linux host)

Use this when you'd rather run the bare binary — lower memory footprint, no Docker daemon, easy on a Pi or small VPS.

### 1. Build for the target architecture

On your dev machine:

```bash
make linux-amd64          # x86_64 server / VPS
make linux-arm64          # ARM64: Pi 3/4/5 with 64-bit OS, AWS Graviton, Oracle Ampere, etc.
make linux-armv7          # ARMv7: Pi 2/3 with 32-bit OS
make linux-armv6          # ARMv6: Pi Zero / Pi 1
```

Binary lands in `target/<arch>/recall-server`. Check the host's architecture with `uname -m`:

| `uname -m` | Target |
|---|---|
| `x86_64` | `linux-amd64` |
| `aarch64` | `linux-arm64` |
| `armv7l` | `linux-armv7` |
| `armv6l` | `linux-armv6` |

Cross-compiling uses `zig cc` for CGO (`go-sqlite3`). Install Zig with `brew install zig` on macOS or your distro's package manager on Linux.

### 2. Copy artifacts to the host

Replace `myhost` with your server's hostname or IP, and `linux-arm64` with the arch you built.

```bash
scp target/linux-arm64/recall-server user@myhost:/tmp/
scp deploy/recall.service             user@myhost:/tmp/
scp config.example.yaml               user@myhost:/tmp/config.yaml
scp -r seed                            user@myhost:/tmp/
```

### 3. Install on the host

```bash
ssh user@myhost
sudo useradd --system --home /opt/recall --shell /usr/sbin/nologin recall || true
sudo mkdir -p /opt/recall/data /opt/recall/seed
sudo mv /tmp/recall-server /opt/recall/
sudo mv /tmp/config.yaml   /opt/recall/
sudo mv /tmp/seed/*        /opt/recall/seed/ 2>/dev/null || true
sudo chown -R recall:recall /opt/recall
sudo chmod +x /opt/recall/recall-server

# Edit config (set session_secret, deepl.api_key, etc.)
sudo -u recall nano /opt/recall/config.yaml

sudo mv /tmp/recall.service /etc/systemd/system/recall.service
sudo systemctl daemon-reload
sudo systemctl enable --now recall.service
```

### 4. Verify

```bash
systemctl status recall
journalctl -u recall -f
curl http://localhost:8080/
```

### Updating

```bash
scp target/linux-arm64/recall-server user@myhost:/tmp/
ssh user@myhost 'sudo install -o recall -g recall -m 755 /tmp/recall-server /opt/recall/recall-server && sudo systemctl restart recall'
```

## Reverse proxy & TLS

Either path serves plain HTTP on the configured port (default `:8080`). For internet-facing deployments, terminate TLS at a reverse proxy (Caddy, nginx, Traefik) and proxy to `127.0.0.1:8080`. Recall is a stateful single-process app — do not run multiple replicas against the same SQLite file.
