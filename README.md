
# go-scdo
[![Build Status](https://travis-ci.org/scdo/go-scdo.svg?branch=master)](https://travis-ci.org/scdo/go-scdo)

|        Features        |      Descriptions                                                                              |
|:-----------------------|------------------------------------------------------------------------------------------------|
| **Sharding**           | 4 shards, transactions within the same shard and between different shards are supported<br/> higher transaction fee for cross-shard transaction                                  |
| **Smart Contracts**    | smart contracts are supported within the same shard                                          |
| **SCDO Wallet**       | easy-to-use wallet                                                                             |
| **High TPS**           | same shard TPS: 250/shard, cross shard TPS: 6/shard                                           |
| **Auditable Supply**   | total supply: 300,000,000 SCDO, all from mining                              |
| **Consensus Algorithm**| ZPOW algorithm                                                |
| **Mining Reward**      | 3150000 blocks/era and block reward at each era follows [6, 4, 3, 2.5, 2, 2, 1.5, 1.5] order until it reaches the last reward of 1.5 SCDO |
| **Transaction Fee**    | self-customized transaction fee, higher fee for cross-shard transaction                        |
| **Block**              | 100 KB block size, 20 seconds block time, ~6000 transactions per block                         |


The official Golang implementation of SCDO. SCDO is an open source blockchain project which consists of advanced sharding technology, innovative ZPoW consensus algorithm and scalable subchain protocol. [https://scdoscan.io](https://scdoscan.io)

The current release: SCDO Classic (the original non-EVM sharded chain, with addresses such as `1S01…`) is powered by a new anti-ASIC consensus PoW algorithm, which requires scientific calculation related to randomized matrix. SCDO Classic has four shards, named SCDO Shard1 (Classic), SCDO Shard2 (Classic), SCDO Shard3 (Classic) and SCDO Shard4 (Classic), and they are producing blocks. Users can perform transactions within a shard or across shards. However, currently smart contracts can only be executed within the same shard. SCDO Shard0 (EVM) is the separate EVM network (chain ID 5680, RPC https://scdoscan.io/rpc/0), see [SCDOLAB/scdo-shard0](https://github.com/SCDOLAB/scdo-shard0).

# Download (without building)

Prebuilt binaries for running a node without a Go toolchain:

| Operating system | Download |
|---|---|
| Linux x86_64 | <https://scdoscan.io/downloads/>: [`scdo-node-linux-amd64`](https://scdoscan.io/downloads/scdo-node-linux-amd64), [`scdo-client-linux-amd64`](https://scdoscan.io/downloads/scdo-client-linux-amd64), [`SHA256SUMS`](https://scdoscan.io/downloads/SHA256SUMS) |
| Linux / macOS / Windows (older releases) | <https://github.com/scdoproject/go-scdo/releases> |

Verify what you download before running it:

```bash
curl -fsSLO https://scdoscan.io/downloads/SHA256SUMS
curl -fLO https://scdoscan.io/downloads/scdo-node-linux-amd64
curl -fLO https://scdoscan.io/downloads/scdo-client-linux-amd64
sha256sum -c SHA256SUMS
```

The directory has no browsable index: use the file links above.

## Solo mining (no pool)

There is no mining pool for SCDO Classic (the project-run PPLNS pool at <https://scdoscan.io/pool/> is for SCDO Shard0 (EVM) only). To mine, run your own full node for your shard; block rewards
(currently 3 SCDO per block) go directly to your address. One command on Linux x86_64:

```bash
curl -fsSL https://scdoscan.io/mine.sh -o mine.sh && bash mine.sh
```

The same script is in [`scripts/mine.sh`](scripts/mine.sh). Step-by-step guide:
[`docs/quickstart.md`](docs/quickstart.md) (live version: <https://scdoscan.io/quickstart.html>).

## Public seed nodes

The sample configs in `cmd/node/config/node1-4.json` list the public P2P nodes
(TCP + UDP). One port per shard:

| Shard | P2P port | Hosts |
|---|---|---|
| 1 | 8057 | 74.208.207.184, 82.223.19.88, 74.208.136.152, 217.160.65.210 |
| 2 | 8058 | same hosts |
| 3 | 8059 | same hosts |
| 4 | 8056 | same hosts |

The sample configs leave `privateKey` empty. On first start the node writes a unique
p2p key to `<dataDir>/p2p.key` (mode 0600). You can still set `privateKey` yourself.
Create your wallet (coinbase) with `node key --shard N` — that key is not the p2p key.
Keep the printed private key safe and put the address in `basic.coinbase` before mining.

# Or Download & Build the source

This tree is **Scdo_V2.0.0**. A public V1.0.0 binary from 2021 is not this code.
Building needs a current Go toolchain (1.22 or newer; modules, `go build -mod=vendor`), Git and a C compiler (for libsecp256k1). GPU mining is optional: the default build does not link `libgoGpuDet.so`, so `node -v` and sync work without it. Rebuild with `-tags gpu` and place `libgoGpuDet.so` next to the binary if you mine on a GPU.

```bash
git clone https://github.com/scdoproject/go-scdo.git
cd go-scdo
make node client
./build/node -v
```

Windows (from a shell with `gcc` on `PATH`, for example MinGW):

```bat
buildall.bat
```

Tagged releases `v*` (including `v2.0.0`) build Linux and Windows `node` and `client` binaries in GitHub Actions and attach them to the release. Pushing the tag is what publishes those assets.

# Run SCDO

SCDO Classic is a fork coin. A fresh node starts at fork genesis height 2979594. That height is the chain start, not a snapshot and not a bug. The node then full-syncs later blocks and logs current height, peer target, blocks/min and ETA.

First run:

```bash
./build/node key --shard 1
# prints Account and private key. Save the private key. Put the account in basic.coinbase.
./build/node start -c cmd/node/config/node1.json
```

`node start` only syncs. Add `-m start --threads N` to mine. `scripts/mine.sh` already passes `-m start`.

Data directory: a relative `dataDir` is created under `$HOME/.scdo` (`%USERPROFILE%\.scdo` on Windows). An absolute `dataDir`, or `--datadir`, is used as-is. The IPC socket is created inside that directory so two nodes do not share one socket.

A simple version SCDO mining tutorial: [SCDO Mining Tutorial (English)](https://scdo-project.gitbook.io/scdo-wiki/en/mining), [SCDO Mining Tutorial (Chinese)](https://scdo-project.gitbook.io/scdo-wiki/zhong-wen/wa-kuang).

For running a node, please refer to [Get Started](https://scdo-project.gitbook.io/scdo-wiki/developer/go-scdo/gettingstarted).
For more usage details and deeper explanations, please consult the [SCDO Wiki](https://scdo-project.gitbook.io/scdo-wiki/).

# Contribution

Thank you for considering helping out with our source code. We appreciate any contributions, even the smallest fixes.

Here are some guidelines before you start:
* Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting) guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
* Pull requests need to be based on and opened against the `master` branch.
* We use reviewable.io as our review tool for any pull request. Please submit and follow up on your comments in this tool. After you submit a PR, there will be a `Reviewable` button in your PR. Click this button, it will take you to the review page (it may ask you to login).
* If you have any questions, feel free to join [Twitter](https://twitter.com/OfficialScdo?s=20) to communicate with our core team.

# Resources

* [SCDO Website](https://scdoscan.io/)
* [Telegram Group](https://t.me/scdogroup)
* [Explorer and downloads](https://scdoscan.io/)
* [SCDO Wiki](https://scdo-project.gitbook.io/scdo-wiki/)
* [scdo-sdk-javascript](https://www.npmjs.com/package/scdo-sdk-javascript)
* [Twitter](https://twitter.com/OfficialScdo?s=20)

# License

[go-scdo/LICENSE](https://github.com/scdoproject/go-scdo/blob/master/LICENSE)
