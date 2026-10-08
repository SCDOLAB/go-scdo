# Mobile node (Classic shards 1–4)

This is the API for the Android wallet [SCDOLAB/scdo-wallet-mobile](https://github.com/SCDOLAB/scdo-wallet-mobile). The phone runs one of two modes. Both start at fork genesis height **2979594**, check every header with ZPoW, and do not use a snapshot, checkpoint, or assumevalid shortcut.

**Lite** is the simple UI. It header-syncs the selected shards, keeps the last 10,000 headers plus an accumulator, and loads accounts, transactions and cross-shard debts with Merkle proofs. The steady store stays under 1 GB.

**Pro** downloads full blocks and state for the shards the user picks and validates them from fork genesis. Lite header databases are left in place, so a switch from Lite to Pro does not throw away headers that already checked out.

## Embed the Go package

The process the app embeds is package `mobile`.

```text
gomobile bind -target=android -o scdo.aar github.com/scdoproject/go-scdo/mobile
```

The bind needs the Android NDK and a C compiler. Peer identity uses libsecp256k1. The database is pure Go (LevelDB).

| Call | What the wallet gets |
| --- | --- |
| `Start(dataDir, mode, shards)` | `mode` is `lite` or `pro`. `shards` is `1,2,3,4` (empty means all four). A relative `dataDir` is placed under `$HOME/.scdo`. |
| `Stop()` | Shut the node down. Databases stay on disk. |
| `Status()` / `Syncing()` | JSON array, one object per running shard: height, peer height, peers, network, ETA, disk used. |
| `Balance(address)` | JSON account proof for that address's shard. |
| `TxProof(txHash)` | JSON transaction inclusion proof. |
| `DebtProof(debtHash)` | JSON cross-shard debt proof. |
| `Estimate(head)` | JSON storage and bandwidth, including the Pro estimate. `head` 0 uses the sample below. |
| `Pause(reason)` / `Resume()` | Stop or continue downloads. The verified tip stays on disk. |
| `SetSyncPolicy(metered, lowBattery)` | Choose which device states pause sync. `Start` allows 5G and pauses on a low battery. |
| `SetDeviceState(metered, lowBattery)` | Tell the node what Android reported. Go cannot read the battery or the metered flag itself. |
| `VerifyHeader` / `VerifyTx` / `VerifyDebt` | Check an older proof against the hash accumulator this phone built. |
| `VerifyAccount(stateRoot, accountKey, proofJSON)` | Re-check a balance proof in-process. No network. |

`Start` in lite mode opens JSON-RPC on `127.0.0.1:18037` and listens for peers on `0.0.0.0:18057`. Pro gives each shard its own peer port (`18057` plus the shard index, so shard 1 is `18057` and shard 4 is `18060`). The first selected shard keeps `127.0.0.1:18037`. The others use `127.0.0.1:(18037+shard)`. The same light methods are available as `light_*` if the app talks HTTP instead of the AAR. A desktop node serves them too:

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
    "forkGenesis": 2979594,
    "paused": false,
    "network": "metered-allowed",
    "eta": "unknown",
    "diskBytes": 0,
    "retained": 10000,
    "mmrLeaves": 1
  }
]
```

`height` starts at 2979594 because that is the fork genesis, not block 0. `syncing` is true while a header batch is being written. Each write checks the parent link, the difficulty rule, and the ZPoW determinant. A header that fails those checks is not stored. `mmrLeaves` counts headers whose hashes were committed after that check. `retained` is how many recent headers stay on disk per shard, plus fork genesis.

When a peer announces a higher head, the client starts a sync immediately. A 13 second poll is the backup. If a session starts and then dies while the phone is still behind, it retries after 2 seconds. A dropped session does not roll the verified tip backwards. After the phone has caught up, a new block (about every 20 seconds) is the real-time path. 5G delay is the peer round trip, not a multi-block wait.

`network` is `unmetered`, `metered-allowed`, or `paused:` plus the reason (`low-battery`, `metered`, or the text passed to `Pause`). `eta` is `paused`, `0s` once the phone has caught the peer, a duration once two `Status` samples show a rate, or `unknown` until then. In pro mode a running full download also reports the downloader's own ETA. `diskBytes` is the size of that shard's directory. Pro adds `headerDiskBytes` for the lite database, which is not deleted.

`light_pause`, `light_resume`, `light_setSyncPolicy` and `light_setDeviceState` control the same gate as the AAR methods. Pausing cancels the download in progress and leaves the stored tip where it is. `Start` on the phone allows a metered network, because 5G is the real-time path, and pauses when the app reports a low battery. Call `SetSyncPolicy(true, true)` if a metered network should pause too. A desktop `node start -l` does not pause for either until something calls `SetSyncPolicy`. Low battery wins over a metered network when both are set.

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

The proof is the transaction Merkle trie under that header's `txRoot`. For a transaction inside the retained window the header is one this phone still stores. `confirmed` means the transaction is at least 120 blocks behind that shard's head (`ConfirmedBlockNumber`). That count is the existing finality rule, not a new one. `included` false means the transaction is still only in the pool, so there is no trie proof yet.

A transaction older than the retained window is checked with `light_verifyTx` (AAR `VerifyTx`). A full node serves `light_getHeaderProof(height, accumulatorHead)`, where `accumulatorHead` is this phone's verified height from `light_syncing`. The phone checks the header hash against its own accumulator, then checks the transaction trie against that header's `txRoot`. The same path is `light_verifyDebt` / `VerifyDebt` for a cross-shard debt. The phone does not accept a header it did not verify itself.

## `light_getDebtProof`

```json
{"jsonrpc":"2.0","method":"light_getDebtProof","params":["0xabc..."],"id":1}
```

Same shape as the transaction proof, plus `debtRoot` and `confirmationsNeed` (120). The debt leaf is proven against the header's debt root. A cross-shard spend on a full node already waits until the source shard has 120 blocks after the source transaction. The phone reports that same gap from the header chain it verified. It does not lower the confirmation count.

## Storage and bandwidth

Measured from a live shard-1 header at height 9,275,180 on 2026-10-07 (`TestPhoneEstimateFitsALargePhone`). Every header from fork genesis is still downloaded and checked. After the check, each shard keeps fork genesis plus the last 10,000 headers (about 55 hours, more than the 120-confirmation window) and an MMR snapshot of the verified header hashes for that window.

| | |
| --- | --- |
| Headers verified per shard | 9,275,180 − 2,979,594 = 6,295,586 |
| Headers kept per shard | 10,000 + fork genesis (10,001) |
| Header on the wire | 259 bytes |
| Header plus difficulty and indexes | 373 bytes |
| Accumulator snapshots, four shards | 31,640,000 bytes |
| Four shards, raw records | 46,561,492 bytes |
| Four shards, phone budget (records × 2) | 93,122,984 bytes (about 89 MiB) |
| One-time download | 6,522,227,096 bytes (about 6.5 GB) |
| After catch-up | 51.8 bytes/second |

The ×2 budget is room for LevelDB indexes and compaction. The exact byte counts are what `light_estimate` and `mobile.Estimate` return; the test logs them. Passing 0 repeats the 2026-10-07 sample. The sample is not a trusted checkpoint. The node still downloads and checks every header from 2979594. It does not keep every header.

On 5G, 6.5 GB at 20–50 Mbps is roughly 20–45 minutes of download, and a dropped transfer resumes from the verified tip. The ZPoW step that dominates verification is a 30×30 determinant, about 17 µs on a server core (`BenchmarkMatrixDet` in `consensus/zpow`). One shard's history is a couple of minutes of that work on that CPU. A phone core is slower, and the four shards verify side by side. Once the phone is caught up, one new header per shard per 20 seconds stays real-time. Steady storage does not grow with the chain: a new verified header pushes the oldest retained header out.

Each light peer keeps at most 2048 header hashes. The full chain of hashes is not retained in memory.

## Pro mode (full blocks and state)

`Start(dataDir, "pro", "1,2")` full-syncs only those shards. Data goes to `dataDir/pro/shardN`. The lite files stay at `dataDir/db/lightchainforshard_N`. Call `Stop` before switching mode. The next `Start` does not delete the other tree, so verified headers survive a move from lite to pro.

Pro validates every block from fork genesis. It does not install a snapshot. Cross-shard debts use the lite header clients, which reopen the same header databases. A shard that is not selected for pro still header-syncs so those debts can be checked. The full nodes do not mine.

LevelDB uses the HDD profile from the desktop sync work: 64 MiB block cache, 32 MiB write buffer, 8 MiB tables, a bloom filter, and no per-table fsync. One shard keeps that profile. Starting more than one full shard scales the chain cache down (16 MiB floor) so four shards do not allocate 512 MiB of cache each. A power loss can drop the tail of a write; the recovery point rebuilds it.

These sizes are estimates for the public head on 2026-10-07, not a measurement from a phone or from a 5400 rpm disk. `Estimate` returns the same numbers in `pro`. Each block body is assumed to be 2048 bytes past the header record (a reward plus a few transfers). Account state is assumed to be 512 MiB per shard. The phone budget multiplies the raw sum by 2 for LevelDB compaction. A chain of reward-only blocks is closer to the header figure plus state.

| | One shard | Four shards |
| --- | --- | --- |
| Headers kept (every header after the fork) | 6,295,586 | 25,182,344 |
| Header records | 2,348,253,578 bytes (2.19 GiB) | 9,393,014,312 bytes |
| Assumed block bodies (2048 bytes each) | 12,893,360,128 bytes (12.01 GiB) | 51,573,440,512 bytes |
| Assumed account state | 536,870,912 bytes (512 MiB) | 2,147,483,648 bytes |
| Raw disk | 15,778,484,618 bytes (14.70 GiB) | 63,113,938,472 bytes (58.78 GiB) |
| Phone disk budget (raw × 2) | 31,556,969,236 bytes (29.39 GiB) | 126,227,876,944 bytes (117.56 GiB) |
| LevelDB memory, full 64 MiB profile | 402,653,184 bytes (384 MiB) | 1,610,612,736 bytes (1.50 GiB) |
| LevelDB memory, cache scaled for four shards |  | 671,088,640 bytes (640 MiB) |

384 MiB is three databases × (64 MiB block cache + two 32 MiB memtables). The scaled figure is what `Start` actually allocates when all four shards are selected: a smaller chain cache and 32 MiB side databases. Lite header databases add up to 32 MiB of block cache each. Pro does not fit a phone that only has a few gigabytes free. Lite is the mode that stays under 1 GB.

## What the wallet should do

1. Call `Start(dir, "lite", "1,2,3,4")` once with an app-private directory. Use `"pro"` and a shard list when the user asks for a full node.
2. Show `Status` until each shard's `height` is near `peerHeight`. Read `network`, `eta` and `diskBytes` from the same objects.
3. Show balance from `Balance`. Keep `headerHash` and `proof` if you want `VerifyAccount` to check them again later.
4. Track a payment with `TxProof`. Treat `confirmed` as the 120-block finality.
5. Track a cross-shard incoming payment with `DebtProof` the same way.
6. Call `SetDeviceState` when the network is metered or the battery is low. The default keeps syncing on 5G and pauses on a low battery. Call `SetSyncPolicy(true, true)` to pause on a metered network as well. Call `Pause` when the app leaves the foreground if you need to release the radio. Call `Resume` and `SetDeviceState(false, false)` to continue. The databases stay on disk, so the next `Start` resumes from the stored tip rather than from genesis.
7. To switch lite to pro, call `Stop`, then `Start(dir, "pro", shards)` with the same directory. Do not delete `db/lightchainforshard_*`.
8. For a payment older than the retained window, ask a full node for `light_getHeaderProof` at that height and this phone's `height`, then `VerifyTx` or `VerifyDebt`.
