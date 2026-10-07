#!/bin/bash
# SCDO solo-mining setup for Linux x86_64 / SCDO 单机挖矿安装脚本 (Linux x86_64)
#
#   curl -fsSL https://scdoscan.io/mine.sh -o mine.sh && bash mine.sh [YOUR_SCDO_ADDRESS]
#
# There is no SCDO mining pool: this script sets up your OWN go-scdo full node that
# syncs your shard and mines; block rewards (3 SCDO per block) go straight to your address.
# 没有矿池：脚本会搭建你自己的全节点，同步完成后挖矿，出块奖励直接进入你的地址。
#
# Options (environment variables):
#   SCDO_DIR=~/scdo-miner   install directory        THREADS=<n>   miner threads (default: all CPUs)
set -euo pipefail

BASE_URL="https://scdoscan.io/downloads"
DIR="${SCDO_DIR:-$HOME/scdo-miner}"
THREADS="${THREADS:-$(nproc)}"
# Public P2P nodes (TCP+UDP). Port per shard: 1=8057, 2=8058, 3=8059, 4=8056.
HOSTS="74.208.207.184 82.223.19.88 74.208.136.152 217.160.65.210"

echo "========================================="
echo "  SCDO solo mining / SCDO 单机挖矿"
echo "========================================="

[ "$(uname -s)" = "Linux" ] && [ "$(uname -m)" = "x86_64" ] || {
  echo "This script supports Linux x86_64 only. Other systems: see https://scdoscan.io/mine.html"; exit 1; }
for t in curl sha256sum; do command -v $t >/dev/null || { echo "Missing tool: $t"; exit 1; }; done

mkdir -p "$DIR"; cd "$DIR"

echo "[1/4] Downloading node + client from $BASE_URL ..."
curl -fsSL "$BASE_URL/SHA256SUMS" -o SHA256SUMS
for f in scdo-node-linux-amd64 scdo-client-linux-amd64; do
  if [ ! -f "$f" ] || ! grep " $f\$" SHA256SUMS | sha256sum -c --status; then
    curl -fL --progress-bar "$BASE_URL/$f" -o "$f"
  fi
done
sha256sum -c SHA256SUMS || { echo "Checksum mismatch - aborting"; exit 1; }
chmod +x scdo-node-linux-amd64 scdo-client-linux-amd64

COINBASE="${1:-}"
if [ -z "$COINBASE" ] && [ -f node.json ]; then
  echo "[2/4] Reusing existing $DIR/node.json"
else
  if [ -z "$COINBASE" ]; then
    read -r -p "Your SCDO address / 你的SCDO地址 (1S01... 2S02... 3S03... 4S04...): " COINBASE < /dev/tty
  fi
  COINBASE="$(echo "$COINBASE" | tr -d '[:space:]')"
  SHARD="${COINBASE:0:1}"
  if ! [[ "$COINBASE" =~ ^[1-4]S0[1-4][0-9a-fA-F]{38}$ ]] || [ "${COINBASE:3:1}" != "$SHARD" ]; then
    echo "Invalid SCDO address: expected 1S01/2S02/3S03/4S04 + 38 hex characters (42 in total)."
    echo "No address yet? Create one with: $DIR/scdo-client-linux-amd64 key --shard 1  (keep the private key safe)"
    exit 1
  fi
  echo "[2/4] Writing node.json for shard $SHARD ..."
  # Node (P2P) key for this shard - separate from your wallet key.
  P2PKEY="$(./scdo-client-linux-amd64 key --shard "$SHARD" | awk 'tolower($1) ~ /^private/ {print $3}')"
  [[ "$P2PKEY" =~ ^0x[0-9a-f]{64}$ ]] || { echo "Failed to generate node key"; exit 1; }
  SEEDS=""
  for port in 8057 8058 8059 8056; do
    for h in $HOSTS; do SEEDS="$SEEDS\"$h:$port\", "; done
  done
  SEEDS="${SEEDS%, }"
  [ -f node.json ] && cp node.json "node.json.bak.$(date +%s)"
  cat > node.json <<JSON
{
    "basic": {
        "name": "SCDO Miner",
        "version": "1.0.0",
        "dataDir": "scdo-shard$SHARD",
        "address": "127.0.0.1:8027",
        "coinbase": "$COINBASE",
        "algorithm": "zpow"
    },
    "p2p": {
        "privateKey": "$P2PKEY",
        "staticNodes": [$SEEDS],
        "address": "0.0.0.0:8057",
        "networkID": "net1"
    },
    "log": {"isDebug": false, "printLog": true},
    "httpServer": {
        "address": "127.0.0.1:8037",
        "crossorigins": ["*"],
        "whiteHost": ["*"]
    },
    "genesis": {
        "difficult": 1900000,
        "shard": $SHARD,
        "timestamp": 1596942480
    }
}
JSON
  chmod 600 node.json
fi

if [ -f scdo.pid ] && kill -0 "$(cat scdo.pid)" 2>/dev/null; then
  echo "A node is already running (pid $(cat scdo.pid)). Stop it first: kill \$(cat $DIR/scdo.pid)"; exit 1
fi

echo "[3/4] Starting node + miner ($THREADS threads) ..."
nohup ./scdo-node-linux-amd64 start -c node.json -m start --threads "$THREADS" >> scdo.log 2>&1 &
echo $! > scdo.pid
sleep 8

echo "[4/4] Status:"
curl -s -m 5 http://127.0.0.1:8037 -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"scdo_getInfo","params":[],"id":1}' || echo "(RPC not up yet - check the log)"
echo
echo "========================================="
echo "  Node started. The first sync downloads the whole shard history"
echo "  SCDO Classic starts at fork genesis height 2979594, then downloads later blocks (~25 GB under $HOME/.scdo) and takes many hours;"
echo "  mining only pays off once CurrentBlockHeight matches https://scdoscan.io"
echo "  首次同步需要下载整个分片历史（约25GB，耗时较长），同步完成后挖矿才有收益。"
echo "  Log / 日志:  tail -f $DIR/scdo.log"
echo "  Stop / 停止: kill \$(cat $DIR/scdo.pid)"
echo "========================================="
