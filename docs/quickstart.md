# SCDO Mining - Quick Start

> Source: the live page <https://scdoscan.io/quickstart.html> (converted to Markdown, 2026-09-28).
> Solo mining with the go-scdo node (ZPoW, CPU), on SCDO Shard1 (Classic), SCDO Shard2 (Classic), SCDO Shard3 (Classic) and SCDO Shard4 (Classic).

| Block reward | Pool |
|---|---|
| 3 SCDO per block | **Solo only**: no pool, no fee |

Live shard heights and average block times: <https://scdoscan.io>.

## Before you start

Mining runs on a PC or server (Windows / Linux / macOS), not on a phone. **There is currently
no mining pool or stratum server**: you run your own full node, it syncs the chain of your
shard, then mines. Block rewards go directly to your address. The first sync downloads the
whole shard history after the fork genesis. SCDO Classic starts at height 2979594
(that is the chain start, not a snapshot). From there the node downloads later
blocks (roughly 25 GB) and can take many hours. Logs show local height, peer
target, blocks/min and ETA.

Node software:

- Linux x86_64: [`scdo-node`](https://scdoscan.io/downloads/scdo-node-linux-amd64) +
  [`scdo-client`](https://scdoscan.io/downloads/scdo-client-linux-amd64)
  ([`SHA256SUMS`](https://scdoscan.io/downloads/SHA256SUMS)), or use the one-command setup below.
- Windows / macOS: [go-scdo releases](https://github.com/scdoproject/go-scdo/releases)
  (contains `node` and `client`), or the Windows one-click miner package from the SCDO team.

## Fastest: Linux one command

```bash
curl -fsSL https://scdoscan.io/mine.sh -o mine.sh && bash mine.sh
```

Linux x86_64, no root needed. It downloads `scdo-node` + `scdo-client` from
`scdoscan.io/downloads` (checksum-verified), asks for your address, creates a node key for
your shard with `node key --shard N` (or `client key --shard N`), writes `node.json`
with the public seed nodes and starts the node + miner. The script is also in this
repository: [`scripts/mine.sh`](../scripts/mine.sh).

Windows: use the one-click miner package (`start-miner.bat`). Or set things up by hand below.

## Manual setup

### 1. Your SCDO address

The prefix decides your shard (`1S01` = shard 1, `2S02` = shard 2, `3S03` = shard 3,
`4S04` = shard 4). No address yet? Run `node key --shard 1` (or `client key --shard 1`) and keep the private key safe.

### 2. Save this as `node.json`

Example for shard 1 (set `coinbase` to your address and `genesis.shard` to your shard):

```json
{
  "basic": {
    "name": "SCDO Miner",
    "version": "2.0.0",
    "dataDir": "scdo-data",
    "address": "0.0.0.0:8027",
    "coinbase": "1S01YOUR_ADDRESS_38_HEX_CHARS",
    "algorithm": "zpow"
  },
  "p2p": {
    "privateKey": "",
    "staticNodes": [
      "74.208.207.184:8057", "82.223.19.88:8057", "74.208.136.152:8057", "217.160.65.210:8057",
      "74.208.207.184:8058", "82.223.19.88:8058", "74.208.136.152:8058", "217.160.65.210:8058",
      "74.208.207.184:8059", "82.223.19.88:8059", "74.208.136.152:8059", "217.160.65.210:8059",
      "74.208.207.184:8056", "82.223.19.88:8056", "74.208.136.152:8056", "217.160.65.210:8056"
    ],
    "address": "0.0.0.0:8057",
    "networkID": "net1"
  },
  "log": { "isDebug": false, "printLog": true },
  "httpServer": {
    "address": "127.0.0.1:8037",
    "crossorigins": ["*"],
    "whiteHost": ["*"]
  },
  "genesis": { "difficult": 1900000, "shard": 1, "timestamp": 1596942480 }
}
```

Leave `privateKey` empty. The node generates a unique p2p key on first start and
saves it in the data directory as `p2p.key`. That key is only for the network
identity. Your wallet key comes from `node key --shard N`.

Public P2P seed nodes (TCP + UDP), one port per shard:

| Shard | Port | Hosts |
|---|---|---|
| 1 | 8057 | 74.208.207.184, 82.223.19.88, 74.208.136.152, 217.160.65.210 |
| 2 | 8058 | 74.208.207.184, 82.223.19.88, 74.208.136.152, 217.160.65.210 |
| 3 | 8059 | 74.208.207.184, 82.223.19.88, 74.208.136.152, 217.160.65.210 |
| 4 | 8056 | 74.208.207.184, 82.223.19.88, 74.208.136.152, 217.160.65.210 |

### 3. Start the node + miner

`node start` syncs only. Mining is opt-in with `-m start`.

Data files go to `$HOME/.scdo/<dataDir>` unless `dataDir` is absolute or you pass
`--datadir`. `$HOME` / `%USERPROFILE%` is honored.

```bash
# Linux / macOS — sync
./node start -c node.json
# then mine
./node start -c node.json -m start --threads 4
# Windows
node.exe start -c node.json -m start --threads 4
```

Check progress:

```bash
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"scdo_getInfo","params":[],"id":1}' http://127.0.0.1:8037
```

Mining becomes useful once `CurrentBlockHeight` matches the explorer.

## How it works

1. Your node connects to the public SCDO P2P nodes for your shard.
2. It downloads and verifies the full shard history.
3. It mines new blocks with your CPU; each block you find pays 3 SCDO to your address.
4. Track your address at [scdoscan.io](https://scdoscan.io) (`https://scdoscan.io/account/detail?address=<your address>`).
