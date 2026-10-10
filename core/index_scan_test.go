/**
* @file
* @copyright defined in scdo/LICENSE
 */

package core

import (
	"math/big"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
)

// countingStore counts full-block reads. The height-index scan must not use
// them when the height-to-hash key and the header are present.
type countingStore struct {
	store.BlockchainStore
	bodies int
}

func (c *countingStore) GetBlock(hash common.Hash) (*types.Block, error) {
	c.bodies++
	return c.BlockchainStore.GetBlock(hash)
}

func (c *countingStore) GetBlockByHeight(height uint64) (*types.Block, error) {
	c.bodies++
	return c.BlockchainStore.GetBlockByHeight(height)
}

func TestIndexGapAtUsesTheHeightKey(t *testing.T) {
	bc := NewTestBlockchain()
	st := bc.GetStore()
	height := bc.CurrentBlock().Header.Height + 1
	if !indexGapAt(st, height) {
		t.Fatal("missing height was reported as indexed")
	}
	hash := common.StringToHash("height-only")
	if err := st.PutBlockHash(height, hash); err != nil {
		t.Fatal(err)
	}
	if indexGapAt(st, height) {
		t.Fatal("a height key without a body was treated as a gap")
	}
}

func TestRecoverHeightIndicesDoesNotReadBodies(t *testing.T) {
	bc := NewTestBlockchain()
	wrapped := &countingStore{BlockchainStore: bc.bcStore}
	bc.bcStore = wrapped

	fork := uint64(common.SecondForkHeight)
	head := fork + recoverDepth + 10
	var top *types.Block
	prev := bc.CurrentBlock().HeaderHash
	for i := uint64(0); i <= 5; i++ {
		h := head - (5 - i)
		header := &types.BlockHeader{
			PreviousBlockHash: prev,
			Height:            h,
			Difficulty:        big.NewInt(1),
			CreateTimestamp:   big.NewInt(int64(h)),
		}
		block := &types.Block{Header: header, HeaderHash: header.Hash()}
		if err := bc.bcStore.PutBlock(block, big.NewInt(int64(i+1)), true); err != nil {
			t.Fatal(err)
		}
		prev = block.HeaderHash
		top = block
	}
	bc.UpdateCurrentBlock(top)
	before := wrapped.bodies
	bc.recoverHeightIndices(head - 5)
	if wrapped.bodies != before {
		t.Fatalf("index scan read %d full blocks", wrapped.bodies-before)
	}
}
