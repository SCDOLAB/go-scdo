package light

import (
	"math/big"
	"testing"

	"github.com/scdoproject/go-scdo/api"
	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database/leveldb"
)

func TestProveHeaderNotReadyVsMismatch(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := store.NewBlockchainDatabase(db)

	want := common.StringToHash("source-shard-header")
	resp := &OdrProvableResponse{BlockIndex: &api.BlockIndex{BlockHash: want, BlockHeight: 42}}
	_, err := resp.proveHeader(bcStore)
	if !errors.IsOrContains(err, types.ErrHeaderNotReady) {
		t.Fatalf("missing height should wait, got %v", err)
	}

	header := &types.BlockHeader{
		PreviousBlockHash: common.StringToHash("parent"),
		Difficulty:        big.NewInt(1),
		Height:            42,
		CreateTimestamp:   big.NewInt(1),
	}
	// Force the stored header hash to `other` by writing that header, then
	// request a proof for a different hash at the same height.
	stored := header.Hash()
	block := &types.Block{HeaderHash: stored, Header: header}
	if err = bcStore.PutBlock(block, big.NewInt(1), true); err != nil {
		t.Fatal(err)
	}
	resp.BlockIndex.BlockHash = want
	_, err = resp.proveHeader(bcStore)
	if err != types.ErrBlockHashMismatch && !errors.IsOrContains(err, types.ErrBlockHashMismatch) {
		t.Fatalf("canonical hash mismatch should fail hard, got %v", err)
	}

	resp.BlockIndex.BlockHash = stored
	got, err := resp.proveHeader(bcStore)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a header")
	}
	gotHash := got.Hash()
	if !gotHash.Equal(stored) {
		t.Fatalf("expected stored header %s, got %s", stored.Hex(), gotHash.Hex())
	}
}
