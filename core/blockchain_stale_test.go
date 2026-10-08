package core

import (
	"math/big"
	"strings"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database/leveldb"
)

func putCanonical(t *testing.T, bcStore store.BlockchainStore, height uint64, parent common.Hash) common.Hash {
	t.Helper()
	header := &types.BlockHeader{
		PreviousBlockHash: parent,
		Difficulty:        big.NewInt(1),
		Height:            height,
		CreateTimestamp:   big.NewInt(1),
	}
	hash := header.Hash()
	block := &types.Block{HeaderHash: hash, Header: header}
	if err := bcStore.PutBlock(block, big.NewInt(int64(height)), true); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestOverwriteStaleBlocksForkGenesis(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := store.NewBlockchainDatabase(db)

	preFork := common.StringToHash("seele-parent-before-fork")
	genesisHash := putCanonical(t, bcStore, genesisBlockHeight, preFork)
	childHash := putCanonical(t, bcStore, genesisBlockHeight+1, genesisHash)

	genesisCanonical, err := bcStore.GetBlockHash(genesisBlockHeight)
	if err != nil {
		t.Fatal(err)
	}
	childCanonical, err := bcStore.GetBlockHash(genesisBlockHeight + 1)
	if err != nil {
		t.Fatal(err)
	}

	if err = OverwriteStaleBlocks(bcStore, genesisHash, nil); err != nil {
		t.Fatalf("overwrite of canonical genesis should stop before the unstored Seele parent: %v", err)
	}
	if err = OverwriteStaleBlocks(bcStore, preFork, nil); err != nil {
		t.Fatalf("recovery of the pre-fork parent hash should succeed: %v", err)
	}
	if err = OverwriteStaleBlocks(bcStore, childHash, nil); err != nil {
		t.Fatalf("overwrite starting at the first post-genesis block should succeed: %v", err)
	}

	gotGenesis, err := bcStore.GetBlockHash(genesisBlockHeight)
	if err != nil {
		t.Fatal(err)
	}
	gotChild, err := bcStore.GetBlockHash(genesisBlockHeight + 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotGenesis != genesisCanonical || gotChild != childCanonical {
		t.Fatalf("canonical map changed: genesis %s child %s", gotGenesis.Hex(), gotChild.Hex())
	}

	missing := common.StringToHash("missing-header-not-in-the-store")
	err = OverwriteStaleBlocks(bcStore, missing, nil)
	if err == nil {
		t.Fatal("expected an error for a missing header in the middle of a walk")
	}
	if !strings.Contains(err.Error(), missing.Hex()) {
		t.Fatalf("error should name the missing hash %s, got %s", missing.Hex(), err.Error())
	}
}
