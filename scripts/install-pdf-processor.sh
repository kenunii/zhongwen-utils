#!/usr/bin/env bash
# Installs only the independent worker. Never signals xochitl or Xovi.
set -euo pipefail
if [[ $# != 2 ]]; then
    echo "Usage: $0 SSH_HOST PRIVATE_CREDENTIALS_JSON" >&2
    exit 2
fi
host=$1
credentials=$(realpath "$2")
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output="$root/build/pdf-processor"
mkdir -p "$output"
python3 "$root/scripts/sync-processor-contract.py" --check
(cd "$root/processor" && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
    go build -trimpath -ldflags='-s -w' -o "$output/duchinese-pdf-processor" .)
# The optional private vpn object selects the reproducible userspace runtime.
vpn_files=()
if python3 - "$credentials" <<'PYCONFIG'
import json, sys
with open(sys.argv[1]) as source:
    config = json.load(source)
sys.exit(0 if config.get('vpn') else 1)
PYCONFIG
then
    python3 "$root/scripts/build-pdf-vpn.py"
    vpn_files=(-C "$root/build/vpn-runtime" runtime)
fi
ssh_options=(-o ConnectTimeout=5 -o ServerAliveInterval=5 -o ServerAliveCountMax=2)
stage=$(ssh "${ssh_options[@]}" "$host" 'umask 077; mkdir -p /home/root/.cache/duchinese-native-update; mktemp -d /home/root/.cache/duchinese-native-update/processor.XXXXXX')
[[ $stage =~ ^/home/root/\.cache/duchinese-native-update/processor\.[A-Za-z0-9]+$ ]]
# Send secrets via stdin, never command-line arguments or logs.
tar -C "$output" -cf - duchinese-pdf-processor \
    -C "$root/packaging/processor" duchinese-pdf-processing.service "${vpn_files[@]}" |
    ssh -C "${ssh_options[@]}" "$host" "tar -xf - -C '$stage'"
ssh "${ssh_options[@]}" "$host" "umask 077; cat > '$stage/credentials.json'" < "$credentials"
ssh "${ssh_options[@]}" "$host" sh -s -- "$stage" <<'REMOTE'
set -eu
stage=$1
mkdir -p /home/root/.local/bin /home/root/.config/duchinese-pdf-processing /home/root/.cache/duchinese-deploy
exec 9>/home/root/.cache/duchinese-deploy/install.lock
flock -n 9
pid=$(systemctl show xochitl -p MainPID --value)
if test -d "$stage/runtime"; then
    # Stop only our worker (and its VPN child) before replacing its private libs.
    systemctl stop duchinese-pdf-processing.service || true
    mkdir -p /home/root/.local/lib
    if test -d /home/root/.local/lib/duchinese-vpn; then
        mv /home/root/.local/lib/duchinese-vpn "$stage/previous-vpn"
    fi
    mv "$stage/runtime" /home/root/.local/lib/duchinese-vpn
fi
chmod 600 "$stage/credentials.json"
chmod 755 "$stage/duchinese-pdf-processor"
# Renaming avoids modifying an executable that is currently running.
mv "$stage/duchinese-pdf-processor" /home/root/.local/bin/duchinese-pdf-processor
mv "$stage/credentials.json" /home/root/.config/duchinese-pdf-processing/credentials.json
cp "$stage/duchinese-pdf-processing.service" /etc/systemd/system/duchinese-pdf-processing.service
systemctl daemon-reload
systemctl enable duchinese-pdf-processing.service
systemctl restart duchinese-pdf-processing.service
sleep 1
systemctl is-active duchinese-pdf-processing.service
test "$(systemctl show xochitl -p MainPID --value)" = "$pid"
echo "Processor installed. xochitl was not restarted."
rm -f "$stage/duchinese-pdf-processing.service"
rm -rf "$stage/previous-vpn"
rmdir "$stage"
REMOTE
