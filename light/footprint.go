/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"math/big"
	"math/bits"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/types"
)

const (
	// BlockIntervalSec is the difficulty target. A new header is due about
	// this often on each shard.
	BlockIntervalSec = 20

	// SampledClassicHead is shard 1's height on the public network on
	// 2026-10-07. It is only a size example. Sync still starts at fork genesis
	// and checks every header.
	SampledClassicHead uint64 = 9275180

	// levelDBSlack covers snappy blocks, indexes and compaction. The raw
	// record size is what WriteHeader stores; the slack is the phone budget.
	levelDBSlack = 2

	// proBodyBytes is a planning size for one full block beyond the header
	// record: the reward transaction, its receipt, and a few transfers.
	// It is not a measurement of the public chain.
	proBodyBytes = 2048

	// proStateBytes is a planning size for one shard's account trie, contract
	// storage and debt indexes at the 2026-10-07 head. It is not measured here.
	proStateBytes = 512 << 20

	// proDBCount is the chain, account-state and debt databases of one full shard.
	proDBCount = 3

	// proBlockCacheBytes and proWriteBufferBytes are the LevelDB profile from
	// the HDD sync fixes: 64 MiB block cache and a 32 MiB write buffer.
	proBlockCacheBytes  = 64 << 20
	proWriteBufferBytes = 32 << 20

	// proMemtables is the active memtable plus the immutable one LevelDB keeps.
	proMemtables = 2
)

// HeaderDiskBytes is the canonical record for one header: the header, its
// total difficulty, the height-to-hash index, and the key prefixes. Bodies,
// receipts and transaction indexes are not included.
func HeaderDiskBytes(header *types.BlockHeader, td *big.Int) int {
	if header == nil {
		return 0
	}
	if td == nil {
		td = header.Difficulty
	}
	headerBytes := common.SerializePanic(header)
	tdBytes := common.SerializePanic(td)
	const (
		prefix = 1
		hash   = common.HashLength
		height = 8
	)
	return len(headerBytes) + len(tdBytes) + hash +
		(prefix + hash) + (prefix + hash) + (prefix + height)
}

// SyncEstimate is the storage and bandwidth for header-only sync of every shard.
type SyncEstimate struct {
	ForkGenesis           uint64  `json:"forkGenesis"`
	Head                  uint64  `json:"head"`
	HeadersPerShard       uint64  `json:"headersPerShard"`
	Shards                int     `json:"shards"`
	BytesPerHeader        int     `json:"bytesPerHeader"`
	HeaderBytes           int     `json:"headerBytes"`
	RetainedPerShard      uint64  `json:"retainedPerShard"`
	StoredHeadersPerShard uint64  `json:"storedHeadersPerShard"`
	MMRBytes              uint64  `json:"mmrBytes"`
	RawStorageBytes       uint64  `json:"rawStorageBytes"`
	PhoneStorageBytes     uint64  `json:"phoneStorageBytes"`
	DownloadBytes         uint64  `json:"downloadBytes"`
	SteadyBytesPerSecond  float64 `json:"steadyBytesPerSecond"`
	BlockIntervalSec      int     `json:"blockIntervalSec"`

	// Pro is the full-block estimate for the phone's pro mode. Lite storage
	// stays in PhoneStorageBytes. Pro keeps every post-fork block and the
	// account trie. The figures are estimates, not a measurement from a phone.
	Pro ProEstimate `json:"pro"`
}

// ProEstimate is disk and memory for a full Classic shard on a phone.
// DiskBytes is one shard. AllShardsDiskBytes is that figure times ShardCount.
type ProEstimate struct {
	Assumption            string `json:"assumption"`
	HeadersPerShard       uint64 `json:"headersPerShard"`
	HeaderDiskBytes       uint64 `json:"headerDiskBytes"`
	BodyBytesPerBlock     int    `json:"bodyBytesPerBlock"`
	BodyDiskBytes         uint64 `json:"bodyDiskBytes"`
	StateBytes            uint64 `json:"stateBytes"`
	RawDiskBytes          uint64 `json:"rawDiskBytes"`
	DiskBytes             uint64 `json:"diskBytes"`
	AllShardsRawDiskBytes uint64 `json:"allShardsRawDiskBytes"`
	AllShardsDiskBytes    uint64 `json:"allShardsDiskBytes"`
	DatabasesPerShard     int    `json:"databasesPerShard"`
	BlockCacheBytes       uint64 `json:"blockCacheBytes"`
	WriteBufferBytes      uint64 `json:"writeBufferBytes"`
	MemtablesPerDB        int    `json:"memtablesPerDB"`
	MemoryPerShardBytes   uint64 `json:"memoryPerShardBytes"`
	MemoryAllShardsBytes  uint64 `json:"memoryAllShardsBytes"`
	ScaledCachePerShardMB int    `json:"scaledCachePerShardMB"`
	ScaledMemoryAllShards uint64 `json:"scaledMemoryAllShards"`
}

// mmrStateBytes bounds one accumulator snapshot. The retained window can
// contain a leaf count with more peaks than the tip, so the budget uses the
// bit width of the leaf count rather than the tip's popcount.
func mmrStateBytes(leaves uint64) int {
	width := bits.Len64(leaves)
	if width < 1 {
		width = 1
	}
	return 32 + width*(1+common.HashLength)
}

// EstimateSync sizes a header-only sync from fork genesis to head.
// Every header is still downloaded and checked. After that check only the
// last RetainedHeaders per shard, fork genesis, and the accumulator
// snapshots for that window stay on disk. head 0 uses SampledClassicHead.
// headerBytes is the RLP size of one header, used as the download size.
// diskBytes is HeaderDiskBytes.
func EstimateSync(head uint64, headerBytes, diskBytes int) SyncEstimate {
	if head <= common.ScdoForkHeight {
		head = SampledClassicHead
	}
	if headerBytes < 1 {
		headerBytes = 1
	}
	if diskBytes < headerBytes {
		diskBytes = headerBytes
	}
	count := head - common.ScdoForkHeight
	leaves := count + 1
	retained := uint64(RetainedHeaders)
	if count < retained {
		retained = count
	}
	stored := retained + 1 // plus fork genesis
	shards := uint64(common.ShardCount)
	snap := uint64(mmrStateBytes(leaves))
	mmrBytes := retained * snap * shards
	raw := (stored*uint64(diskBytes) + retained*snap) * shards
	est := SyncEstimate{
		ForkGenesis:           common.ScdoForkHeight,
		Head:                  head,
		HeadersPerShard:       count,
		Shards:                common.ShardCount,
		BytesPerHeader:        diskBytes,
		HeaderBytes:           headerBytes,
		RetainedPerShard:      uint64(RetainedHeaders),
		StoredHeadersPerShard: stored,
		MMRBytes:              mmrBytes,
		RawStorageBytes:       raw,
		PhoneStorageBytes:     raw * levelDBSlack,
		DownloadBytes:         count * uint64(headerBytes) * shards,
		SteadyBytesPerSecond:  float64(common.ShardCount*headerBytes) / float64(BlockIntervalSec),
		BlockIntervalSec:      BlockIntervalSec,
	}
	est.Pro = estimatePro(count, diskBytes)
	return est
}

func estimatePro(headers uint64, headerDiskBytes int) ProEstimate {
	headerDisk := headers * uint64(headerDiskBytes)
	bodyDisk := headers * uint64(proBodyBytes)
	raw := headerDisk + bodyDisk + proStateBytes
	shards := uint64(common.ShardCount)
	memoryOne := uint64(proDBCount) * (uint64(proBlockCacheBytes) + uint64(proWriteBufferBytes)*uint64(proMemtables))
	scaledMB := ProChainCacheMB(common.ShardCount)
	// Scaled profile matches scdo.sideDBCacheMB: chain cache is scaledMB and
	// each of the two side databases stays at the 32 MiB floor. The write
	// buffer is half the cache and at most 32 MiB.
	chainWrite := scaledMB / 2
	if chainWrite < 4 {
		chainWrite = 4
	}
	if chainWrite > 32 {
		chainWrite = 32
	}
	scaled := scaledMemory(scaledMB, chainWrite) + 2*scaledMemory(32, 16)
	return ProEstimate{
		Assumption:            "Estimate only, not measured on a phone or a 5400rpm disk. Every header from fork genesis is still fully validated. Header bytes are the 2026-10-07 sample. Each block body is assumed 2048 bytes. Account state is assumed 512 MiB per shard. Phone disk budget multiplies the raw sum by 2 for LevelDB compaction. Memory is 3 LevelDB databases per shard at the HDD profile (64 MiB block cache, 32 MiB write buffer, 2 memtables). Starting all four shards scales that cache down.",
		HeadersPerShard:       headers,
		HeaderDiskBytes:       headerDisk,
		BodyBytesPerBlock:     proBodyBytes,
		BodyDiskBytes:         bodyDisk,
		StateBytes:            proStateBytes,
		RawDiskBytes:          raw,
		DiskBytes:             raw * levelDBSlack,
		AllShardsRawDiskBytes: raw * shards,
		AllShardsDiskBytes:    raw * levelDBSlack * shards,
		DatabasesPerShard:     proDBCount,
		BlockCacheBytes:       proBlockCacheBytes,
		WriteBufferBytes:      proWriteBufferBytes,
		MemtablesPerDB:        proMemtables,
		MemoryPerShardBytes:   memoryOne,
		MemoryAllShardsBytes:  memoryOne * shards,
		ScaledCachePerShardMB: scaledMB,
		ScaledMemoryAllShards: scaled * shards,
	}
}

func scaledMemory(cacheMB, writeMB int) uint64 {
	return uint64(cacheMB+writeMB*proMemtables) << 20
}

// ProChainCacheMB is the chain database block cache for a phone that starts
// n full shards in one process. One shard keeps the 64 MiB HDD profile
// (return 0 so the node applies that default). More shards share the 64 MiB
// budget, with a floor of 16 MiB, so four shards do not allocate 512 MiB
// of chain cache each.
func ProChainCacheMB(n int) int {
	if n <= 1 {
		return 0
	}
	per := 64 / n
	if per < 16 {
		per = 16
	}
	return per
}
