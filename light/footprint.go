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
	return SyncEstimate{
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
}
