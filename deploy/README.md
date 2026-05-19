# Deploying Recall on a Raspberry Pi (systemd)

These steps install the cross-compiled binary as a systemd service.

## 1. Build on your dev machine

```bash
make linux-arm64          # Pi 3 / 4 / 5 with 64-bit Raspberry Pi OS
# or
make linux-armv7          # Pi 2 / 3 with 32-bit OS
```

Binary lands in `target/<arch>/recall-server`.

Check the Pi's architecture if unsure: `uname -m`
(`aarch64` → arm64, `armv7l` → armv7, `armv6l` → armv6.)

## 2. Copy to the Pi

```bash
scp target/linux-arm64/recall-server pi@raspberrypi.local:/tmp/
scp deploy/recall.service           pi@raspberrypi.local:/tmp/
scp config.example.yaml             pi@raspberrypi.local:/tmp/config.yaml
scp -r seed                          pi@raspberrypi.local:/tmp/
```

## 3. Install on the Pi

```bash
ssh pi@raspberrypi.local
sudo useradd --system --home /opt/recall --shell /usr/sbin/nologin recall || true
sudo mkdir -p /opt/recall/data /opt/recall/seed
sudo mv /tmp/recall-server /opt/recall/
sudo mv /tmp/config.yaml   /opt/recall/
sudo mv /tmp/seed/*        /opt/recall/seed/ 2>/dev/null || true
sudo chown -R recall:recall /opt/recall
sudo chmod +x /opt/recall/recall-server

# Edit config (set session_secret, deepl key, etc.)
sudo -u recall nano /opt/recall/config.yaml

sudo mv /tmp/recall.service /etc/systemd/system/recall.service
sudo systemctl daemon-reload
sudo systemctl enable --now recall.service
```

## 4. Verify

```bash
systemctl status recall
journalctl -u recall -f
curl http://localhost:8080/
```

## Updating

```bash
scp target/linux-arm64/recall-server pi@raspberrypi.local:/tmp/
ssh pi@raspberrypi.local 'sudo install -o recall -g recall -m 755 /tmp/recall-server /opt/recall/recall-server && sudo systemctl restart recall'
```
