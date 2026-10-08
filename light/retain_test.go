/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"math/big"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus/pow"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/database/leveldb"
)

func headerAt(height uint64, parent common.Hash) *types.BlockHeader {
	return &types.BlockHeader{
		PreviousBlockHash: parent,
		Creator:           *crypto.MustGenerateRandomAddress(),
		StateHash:         common.StringToHash("StateHash"),
		TxHash:            common.StringToHash("TxHash"),
		Difficulty:        big.NewInt(1),
		Height:            height,
		CreateTimestamp:   big.NewInt(1),
		Witness:           make([]byte, 0),
		ExtraData:         make([]byte, 0),
	}
}

func putCanonical(t *testing.T, lc *LightChain, header *types.BlockHeader, head bool) {
	t.Helper()
	if err := lc.bcStore.PutBlockHeader(header.Hash(), header, big.NewInt(1), head); err != nil {
		t.Fatal(err)
	}
}

func TestFullNodeRewindsPrunedHeaderStore(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := newTestBlockchainDatabase(db)

	genesis := headerAt(common.ScdoForkHeight, common.EmptyHash)
	putCanonical(t, &LightChain{bcStore: bcStore}, genesis, true)
	// The last retained window of a pruned store: fork+1 is gone, the tip remains.
	tip := headerAt(common.ScdoForkHeight+100, genesis.Hash())
	if err := bcStore.PutBlockHeader(tip.Hash(), tip, big.NewInt(2), true); err != nil {
		t.Fatal(err)
	}

	lc, err := openLightChain(bcStore, db, nil, pow.NewEngine(1), true)
	if err != nil {
		t.Fatal(err)
	}
	if lc.currentHeader.Height != common.ScdoForkHeight {
		t.Fatalf("pruned store head = %d, want fork genesis %d", lc.currentHeader.Height, common.ScdoForkHeight)
	}
	head, err := bcStore.GetHeadBlockHash()
	if err != nil {
		t.Fatal(err)
	}
	if head != genesis.Hash() {
		t.Fatalf("head hash = %s, want genesis %s", head, genesis.Hash())
	}
	if _, err = bcStore.GetBlockHash(common.ScdoForkHeight + 100); err == nil {
		t.Fatal("rewound tip height is still canonical")
	}
	if lc.MMRLeaves() != 1 {
		t.Fatalf("accumulator leaves = %d, want 1", lc.MMRLeaves())
	}
}

func TestFullNodeKeepsHeadersPhoneWouldPrune(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := newTestBlockchainDatabase(db)

	genesis := headerAt(common.ScdoForkHeight, common.EmptyHash)
	if err := bcStore.PutBlockHeader(genesis.Hash(), genesis, big.NewInt(1), true); err != nil {
		t.Fatal(err)
	}
	var prev common.Hash = genesis.Hash()
	var prevHeader = genesis
	for h := uint64(common.ScdoForkHeight) + 1; h <= common.ScdoForkHeight+2; h++ {
		next := headerAt(h, prev)
		next.CreateTimestamp = big.NewInt(int64(h))
		if err := bcStore.PutBlockHeader(next.Hash(), next, big.NewInt(int64(h)), h == common.ScdoForkHeight+2); err != nil {
			t.Fatal(err)
		}
		if err := bcStore.PutBlockHash(h, next.Hash()); err != nil {
			t.Fatal(err)
		}
		prev = next.Hash()
		prevHeader = next
	}

	lc, err := openLightChain(bcStore, db, nil, pow.NewEngine(1), true)
	if err != nil {
		t.Fatal(err)
	}
	if lc.currentHeader.Height != prevHeader.Height {
		t.Fatalf("unpruned head = %d, want %d", lc.currentHeader.Height, prevHeader.Height)
	}

	var keptHeight uint64 = common.ScdoForkHeight + 5
	kept := headerAt(keptHeight, genesis.Hash())
	if err := bcStore.PutBlockHeader(kept.Hash(), kept, big.NewInt(1), false); err != nil {
		t.Fatal(err)
	}
	if err := bcStore.PutBlockHash(keptHeight, kept.Hash()); err != nil {
		t.Fatal(err)
	}
	head := keptHeight + uint64(RetainedHeaders)
	if err := lc.pruneOne(head); err != nil {
		t.Fatal(err)
	}
	if _, err := bcStore.GetBlockHash(keptHeight); err != nil {
		t.Fatalf("full node pruned height %d: %s", keptHeight, err)
	}

	lc.retainAll = false
	if err := lc.pruneOne(head); err != nil {
		t.Fatal(err)
	}
	if _, err := bcStore.GetBlockHash(keptHeight); err == nil {
		t.Fatal("phone mode kept a header outside the window")
	}
}

func TestWriteHeaderAboveForkIgnoresMissingGrandparent(t *testing.T) {
	lc, dispose := buildVerifiedChain(t, 2)
	defer dispose()

	parent := lc.CurrentHeader()
	grandHash, err := lc.bcStore.GetBlockHash(parent.Height - 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = lc.bcStore.DeleteBlockHeader(grandHash); err != nil {
		t.Fatal(err)
	}
	next := testHeader(parent.Height+1, parent.Hash())
	if err = lc.WriteHeader(next); err != nil {
		t.Fatalf("WriteHeader above fork with a missing grandparent: %s", err)
	}
	if lc.CurrentHeader().Height != next.Height {
		t.Fatalf("head = %d, want %d", lc.CurrentHeader().Height, next.Height)
	}
}

func TestWriteHeaderIgnoresMissingGrandparent(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := newTestBlockchainDatabase(db)
	h1 := headerAt(1, common.EmptyHash)
	if err := bcStore.PutBlockHeader(h1.Hash(), h1, h1.Difficulty, true); err != nil {
		t.Fatal(err)
	}
	lc, err := newLightChain(bcStore, db, nil, pow.NewEngine(1))
	if err != nil {
		t.Fatal(err)
	}

	h2 := newTestNonGensisBlockHeader(h1, big.NewInt(1), h1.Height+1)
	if err = lc.WriteHeader(h2); err != nil {
		t.Fatal(err)
	}
	h3 := newTestNonGensisBlockHeader(h2, big.NewInt(1), h2.Height+1)
	if err = lc.WriteHeader(h3); err != nil {
		t.Fatal(err)
	}
	if err = lc.bcStore.DeleteBlockHeader(h1.Hash()); err != nil {
		t.Fatal(err)
	}
	h4 := newTestNonGensisBlockHeader(h3, big.NewInt(1), h3.Height+1)
	if err = lc.WriteHeader(h4); err != nil {
		t.Fatalf("WriteHeader with a missing grandparent: %s", err)
	}
	if lc.currentHeader.Height != h4.Height {
		t.Fatalf("head = %d, want %d", lc.currentHeader.Height, h4.Height)
	}
}

func TestReverseStopsWhenCanonicalIndexHasAGap(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := newTestBlockchainDatabase(db)

	var parent common.Hash
	var hashes [3]common.Hash
	for i, h := range []uint64{10, 11, 12} {
		header := headerAt(h, parent)
		hashes[i] = header.Hash()
		if err := bcStore.PutBlockHeader(hashes[i], header, big.NewInt(int64(h)), h == 12); err != nil {
			t.Fatal(err)
		}
		parent = hashes[i]
	}
	if _, err := bcStore.DeleteBlockHash(11); err != nil {
		t.Fatal(err)
	}

	lc, err := newLightChain(bcStore, db, nil, pow.NewEngine(1))
	if err != nil {
		t.Fatal(err)
	}
	before, err := bcStore.GetHeadBlockHash()
	if err != nil {
		t.Fatal(err)
	}
	d := newDownloader(lc)
	if err = d.reverseLightBCstore(10); err == nil {
		t.Fatal("reverse across a gap succeeded")
	}
	after, err := bcStore.GetHeadBlockHash()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("head changed across a refused reverse: %s -> %s", before, after)
	}
	if _, err = bcStore.GetBlockHash(12); err != nil {
		t.Fatalf("height 12 was deleted: %s", err)
	}
}
