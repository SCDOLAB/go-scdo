# Mobile light client (Classic shards 1–4)

This is the API for the Android wallet [SCDOLAB/scdo-wallet-mobile](https://github.com/SCDOLAB/scdo-wallet-mobile). The phone header-syncs all four shards from fork genesis height **2979594**, checks every header with ZPoW, and loads accounts, transactions and cross-shard debts with Merkle proofs. There is no snapshot, checkpoint, or assumevalid shortcut.

## Embed the Go package

The process the app embeds is package `mobile`.

```text
gomobile bind -target=android -o scdo.aar github.com/scdoproject/go-scdo/mobile
```

The bind needs the Android NDK and a C compiler. Peer identity uses libsecp256k1. The rest of the light database is pure Go (LevelDB).

| Call | What the wallet gets |
| --- | --- |
| `Start(dataDir)` | Header-sync shards 1–4 into `dataDir`. A relative path is placed under `$HOME/.scdo`. |
| `Stop()` | Shut the node down. |
| `Syncing()` | JSON array, one object per shard. |
| `Balance(address)` | JSON account proof for that address's shard. |
| `TxProof(txHash)` | JSON transaction inclusion proof. |
| `DebtProof(debtHash)` | JSON cross-shard debt proof. |
| `Estimate(head)` | JSON storage and bandwidth. `head` 0 uses the sample below. |
| `VerifyAccount(stateRoot, accountKey, proofJSON)` | Re-check a balance proof in-process. No network. |

`Start` also opens JSON-RPC on `127.0.0.1:18037` and listens for peers on `0.0.0.0:18057`. The same methods are available as `light_*` if the app talks HTTP instead of the AAR. A desktop node serves them too:

```bash
./build/node start -c cmd/node/config/node1.json -l
```

`-l` does not mine and does not download block bodies.

## `light_syncing`

```json
[
  {
    "shard": 1,
    "height": 2979594,
    "peerHeight": 9275180,
    "peers": 3,
    "syncing": true,
    "mode": "headers",
    "forkGenesis": 2979594
  }
]
```

`height` starts at 2979594 because that is the fork genesis, not block 0. `syncing` is true while a header batch is being written. Each write checks the parent link, the difficulty rule, and the ZPoW determinant. A header that fails those checks is not stored.

When a peer announces a higher head, the client starts a sync immediately. A 13 second poll is the backup. After the phone has caught up, a new block (about every 20 seconds) is the real-time path. 5G delay is the peer round trip, not a multi-block wait.

## `light_getBalance`

```json
{"jsonrpc":"2.0","method":"light_getBalance","params":["1S01dfdbe4d921d507032cb83ee04bb7efc4fd9a51"],"id":1}
```

The node reads the head header of the address's shard (the first byte of the address). It asks a full node for the state-trie proof of that account, checks the proof against `stateRoot`, and only then returns:

```json
{
  "account": "1S01...",
  "shard": 1,
  "balance": 0,
  "nonce": 0,
  "included": false,
  "stateRoot": "0x...",
  "headerHash": "0x...",
  "height": 9275180,
  "accountKey": "0x...",
  "proof": [{"hash": "0x...", "bytes": "0x..."}]
}
```

`included` false means the proof shows the account is absent. The balance is then zero. `headerHash` is a header this phone already verified. `VerifyAccount` runs that same check again inside the app.

`scdo_getBalance` on the home shard still works. Use `light_getBalance` for the other three shards.

## `light_getTxProof`

```json
{"jsonrpc":"2.0","method":"light_getTxProof","params":["0xabc..."],"id":1}
```

The hash does not name a shard, so the phone asks all four. The first shard that has the transaction returns:

```json
{
  "txHash": "0x...",
  "shard": 2,
  "included": true,
  "blockHash": "0x...",
  "blockHeight": 9000000,
  "txRoot": "0x...",
  "confirmations": 275180,
  "confirmed": true,
  "proof": [{"hash": "0x...", "bytes": "0x..."}]
}
```

The proof is the transaction Merkle trie under that header's `txRoot`. The header is one this phone stored. `confirmed` means the transaction is at least 120 blocks behind that shard's head (`ConfirmedBlockNumber`). That count is the existing finality rule, not a new one. `included` false means the transaction is still only in the pool, so there is no trie proof yet.

## `light_getDebtProof`

```json
{"jsonrpc":"2.0","method":"light_getDebtProof","params":["0xabc..."],"id":1}
```

Same shape as the transaction proof, plus `debtRoot` and `confirmationsNeed` (120). The debt leaf is proven against the header's debt root. A cross-shard spend on a full node already waits until the source shard has 120 blocks after the source transaction. The phone reports that same gap from the header chain it verified. It does not lower the confirmation count.

## Storage and bandwidth

Measured from a live shard-1 header at height 9,275,180 on 2026-10-07 (`TestPhoneEstimateFitsALargePhone`):

| | |
| --- | --- |
| Headers after fork genesis, per shard | 9,275,180 − 2,979,594 = 6,295,586 |
| Header on the wire | 259 bytes |
| Header plus difficulty and indexes | 370 bytes |
| Four shards, raw records | about 9.3 GB |
| Four shards, phone budget (records × 2) | about 18.6 GB |
| One-time download | about 6.5 GB |
| After catch-up | about 52 bytes/second (4 headers × 259 bytes / 20 seconds) |

The ×2 budget is room for LevelDB indexes and compaction. Random header hashes do not compress much, so plan on roughly 10–19 GB free. A 128 GB phone can keep all four header chains. A 32 GB phone cannot. This is still the header chain only: a full block is on the order of 100 KB, so four full shards would be hundreds of gigabytes.

On 5G, 6.5 GB at 20–50 Mbps is roughly 20–45 minutes of download. The ZPoW step that dominates verification is a 30×30 determinant, about 17 µs on a server core (`BenchmarkMatrixDet` in `consensus/zpow`). One shard's history is a couple of minutes of that work on that CPU. A phone core is slower, and the four shards verify side by side. Once the phone is caught up, one new header per shard per 20 seconds stays real-time.

`light_estimate` and `mobile.Estimate` return these fields for any head height. Passing 0 repeats the 2026-10-07 sample. The sample is not a trusted checkpoint. The node still downloads and checks every header from 2979594.

## What the wallet should do

1. Call `Start` once with an app-private directory.
2. Show `Syncing` until each shard's `height` is near `peerHeight`.
3. Show balance from `Balance`. Keep `headerHash` and `proof` if you want `VerifyAccount` to check them again later.
4. Track a payment with `TxProof`. Treat `confirmed` as the 120-block finality.
5. Track a cross-shard incoming payment with `DebtProof` the same way.
6. Call `Stop` when the app leaves the foreground if you need to release the radio. The header databases stay on disk, so the next `Start` resumes from the stored tip rather than from genesis.
