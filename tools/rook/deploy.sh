#!/bin/sh
# Deploy a Rook build of echod to a Show as a trial (rc.local puts the previous build back if it will
# not stay up). Usage: tools/rook/deploy.sh [host] [binary]
#   host    defaults to 192.168.1.117
#   binary  defaults to bin/echod-arm (build it first; see the memory/README for the ldflags)
set -e
HOST=${1:-192.168.1.117}
BIN=${2:-bin/echod-arm}
KEY=${ROOK_SSH_KEY:-$HOME/.ssh/techo5_ed25519}
cd "$(dirname "$0")/../.."
[ -f "$BIN" ] || { echo "no $BIN: build it first" >&2; exit 1; }
scp -O -q -i "$KEY" tools/rook/rc.local "root@$HOST:/data/techo5-linux/rc.local"
scp -O -q -i "$KEY" "$BIN" "root@$HOST:/data/rook/echod.new"
ssh -i "$KEY" "root@$HOST" 'chmod +x /data/techo5-linux/rc.local && sh /data/techo5-linux/rc.local'
echo "deployed to $HOST as a trial: follow it with"
echo "  ssh -i $KEY root@$HOST 'tail -f /data/rook/trial.log'"
