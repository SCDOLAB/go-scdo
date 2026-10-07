/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"fmt"
	"sync"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/core/types"
)

// HeaderInclusion is an MMR inclusion proof for one canonical header.
// LeafCount is the phone's verified head (fork genesis through that height),
// not a checkpoint. The phone checks the proof against peaks it built itself.
type HeaderInclusion struct {
	Height    uint64   `json:"height"`
	Hash      string   `json:"hash"`
	Index     uint64   `json:"index"`
	Header    string   `json:"header"`
	Siblings  []string `json:"siblings"`
	LeafCount uint64   `json:"leafCount"`
}

// headerProofAPI serves proofs from a full node, which still has every header.
// A pruned light client does not: it verifies proofs, it does not create them
// for heights it has deleted.
type headerProofAPI struct {
	mu     sync.Mutex
	chain  BlockChain
	leaves []common.Hash
}

// GetHeaderProof proves header height inside the MMR of headers from fork
// genesis through accumulatorHead. Pass the phone's verified height as
// accumulatorHead. Zero uses this node's own canonical head.
func (a *headerProofAPI) GetHeaderProof(height, accumulatorHead uint64) (*HeaderInclusion, error) {
	if a == nil || a.chain == nil {
		return nil, fmt.Errorf("header chain is not available")
	}
	if height < common.ScdoForkHeight {
		return nil, fmt.Errorf("height %d is before fork genesis", height)
	}
	if accumulatorHead == 0 && a.chain.CurrentHeader() != nil {
		accumulatorHead = a.chain.CurrentHeader().Height
	}
	if accumulatorHead < height {
		return nil, fmt.Errorf("accumulator head %d is below the header height %d", accumulatorHead, height)
	}
	leaves, err := a.leavesThrough(accumulatorHead)
	if err != nil {
		return nil, err
	}
	index := height - common.ScdoForkHeight
	siblings, err := ProveHashes(leaves, index)
	if err != nil {
		return nil, err
	}
	block, err := a.chain.GetStore().GetBlockByHeight(height)
	if err != nil {
		return nil, fmt.Errorf("header %d is not on this node: %s", height, err)
	}
	raw, err := common.Serialize(block.Header)
	if err != nil {
		return nil, err
	}
	out := &HeaderInclusion{
		Height:    height,
		Hash:      block.HeaderHash.Hex(),
		Index:     index,
		Header:    hexutil.BytesToHex(raw),
		Siblings:  make([]string, len(siblings)),
		LeafCount: uint64(len(leaves)),
	}
	for i, sib := range siblings {
		out.Siblings[i] = sib.Hex()
	}
	return out, nil
}

func (a *headerProofAPI) leavesThrough(last uint64) ([]common.Hash, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	want := last - common.ScdoForkHeight + 1
	if uint64(len(a.leaves)) >= want && want > 0 {
		hash, err := a.chain.GetStore().GetBlockHash(last)
		if err != nil {
			return nil, err
		}
		if a.leaves[want-1] == hash {
			return a.leaves[:want], nil
		}
		a.leaves = nil
	}
	start := uint64(common.ScdoForkHeight)
	if len(a.leaves) > 0 {
		have := uint64(common.ScdoForkHeight) + uint64(len(a.leaves)) - 1
		hash, err := a.chain.GetStore().GetBlockHash(have)
		if err != nil || hash != a.leaves[len(a.leaves)-1] {
			a.leaves = nil
		} else {
			start = have + 1
		}
	}
	for h := start; h <= last; h++ {
		hash, err := a.chain.GetStore().GetBlockHash(h)
		if err != nil {
			return nil, fmt.Errorf("header %d is not on this node (a pruned light client only keeps the recent window): %s", h, err)
		}
		a.leaves = append(a.leaves, hash)
	}
	return a.leaves[:want], nil
}

func headerFromHex(hex string) (*types.BlockHeader, error) {
	raw, err := hexutil.HexToBytes(hex)
	if err != nil {
		return nil, err
	}
	header := new(types.BlockHeader)
	if err = common.Deserialize(raw, header); err != nil {
		return nil, err
	}
	return header, nil
}

func decodeHashes(hexes []string) ([]common.Hash, error) {
	out := make([]common.Hash, len(hexes))
	for i, hex := range hexes {
		hash, err := common.HexToHash(hex)
		if err != nil {
			return nil, err
		}
		out[i] = hash
	}
	return out, nil
}
