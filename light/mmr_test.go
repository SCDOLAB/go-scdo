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
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/stretchr/testify/assert"
)

func leafHash(i int) common.Hash {
	return common.BytesToHash([]byte{byte(i), byte(i >> 8), 0xab})
}

func TestMMRProveEveryIndex(t *testing.T) {
	for n := 1; n <= 32; n++ {
		var acc mmr
		leaves := make([]common.Hash, n)
		for i := 0; i < n; i++ {
			leaves[i] = leafHash(i + n*100)
			assert.Nil(t, acc.append(leaves[i]))
		}
		for i := 0; i < n; i++ {
			siblings, err := ProveHashes(leaves, uint64(i))
			assert.Nil(t, err)
			assert.Equal(t, true, acc.verify(uint64(i), leaves[i], siblings))
			if len(siblings) == 0 {
				continue
			}
			bad := append([]common.Hash(nil), siblings...)
			bad[0][0] ^= 0xff
			assert.Equal(t, false, acc.verify(uint64(i), leaves[i], bad))
		}
	}
}

func TestMMRRestoreDropsReplacedLeaf(t *testing.T) {
	var acc mmr
	first := leafHash(1)
	second := leafHash(2)
	other := leafHash(3)
	assert.Nil(t, acc.append(first))
	snap := acc.clone()
	assert.Nil(t, acc.append(second))
	siblings, err := ProveHashes([]common.Hash{first, second}, 1)
	assert.Nil(t, err)
	assert.Equal(t, true, acc.verify(1, second, siblings))

	acc = snap
	assert.Nil(t, acc.append(other))
	assert.Equal(t, false, acc.verify(1, second, siblings))
	siblings, err = ProveHashes([]common.Hash{first, other}, 1)
	assert.Nil(t, err)
	assert.Equal(t, true, acc.verify(1, other, siblings))
	siblings, err = ProveHashes([]common.Hash{first, other}, 0)
	assert.Nil(t, err)
	assert.Equal(t, true, acc.verify(0, first, siblings))
}

func testHeader(height uint64, prev common.Hash) *types.BlockHeader {
	return &types.BlockHeader{
		PreviousBlockHash: prev,
		Creator:           common.EmptyAddress,
		StateHash:         common.BytesToHash([]byte{byte(height)}),
		TxHash:            common.BytesToHash([]byte{1, byte(height)}),
		Difficulty:        big.NewInt(1),
		Height:            height,
		CreateTimestamp:   big.NewInt(int64(height)),
	}
}

func buildVerifiedChain(t *testing.T, extra int) (*LightChain, func()) {
	t.Helper()
	db, dispose := leveldb.NewTestDatabase()
	bcStore := store.NewCachedStore(store.NewBlockchainDatabase(db))
	var prev common.Hash
	for i := 0; i <= extra; i++ {
		header := testHeader(common.ScdoForkHeight+uint64(i), prev)
		if err := bcStore.PutBlockHeader(header.Hash(), header, big.NewInt(int64(i+1)), true); err != nil {
			t.Fatal(err)
		}
		prev = header.Hash()
	}
	lc, err := newLightChain(bcStore, db, newOdrBackend(bcStore, 1), pow.NewEngine(1))
	if err != nil {
		t.Fatal(err)
	}
	return lc, dispose
}

func TestPruneKeepsWindowGenesisAndOldProof(t *testing.T) {
	const extra = 8
	lc, dispose := buildVerifiedChain(t, extra)
	defer dispose()
	assert.Equal(t, uint64(extra+1), lc.MMRLeaves())

	stored := lc.GetHeaderByHeight(common.ScdoForkHeight + 1)
	assert.Equal(t, true, stored != nil)
	old := testHeader(common.ScdoForkHeight+1, lc.GetHeaderByHeight(common.ScdoForkHeight).Hash())
	old.TxHash = common.BytesToHash([]byte{7, 7, 7})
	leaves := make([]common.Hash, extra+1)
	for i := 0; i <= extra; i++ {
		header := lc.GetHeaderByHeight(common.ScdoForkHeight + uint64(i))
		assert.Equal(t, true, header != nil)
		leaves[i] = header.Hash()
	}
	siblings, err := ProveHashes(leaves, 1)
	assert.Nil(t, err)

	assert.Nil(t, lc.pruneTo(3))
	assert.Equal(t, true, lc.GetHeaderByHeight(common.ScdoForkHeight) != nil)
	assert.Equal(t, true, lc.GetHeaderByHeight(common.ScdoForkHeight+1) == nil)
	head := lc.CurrentHeader().Height
	assert.Equal(t, true, lc.GetHeaderByHeight(head) != nil)
	assert.Equal(t, true, lc.GetHeaderByHeight(head-2) != nil)
	assert.Equal(t, true, lc.GetHeaderByHeight(head-3) == nil)
	assert.Nil(t, lc.verifyCanonicalHeader(stored, siblings))
	assert.Equal(t, uint64(extra+1), lc.MMRLeaves())

	// A different header at the pruned height must not pass.
	assert.Equal(t, false, stored.Hash() == old.Hash())
	err = lc.verifyCanonicalHeader(old, siblings)
	assert.Equal(t, true, err != nil)

	// Reorg inside the window restores the parent snapshot and drops the old tip leaf.
	parent := lc.GetHeaderByHeight(head - 1)
	replacement := testHeader(head, parent.Hash())
	replacement.TxHash = common.BytesToHash([]byte{9, 9, 9})
	assert.Nil(t, lc.bcStore.PutBlockHeader(replacement.Hash(), replacement, big.NewInt(int64(extra+2)), true))
	lc.currentHeader = replacement
	assert.Nil(t, lc.noteVerified(replacement))
	assert.Equal(t, false, lc.mmr.verify(uint64(extra), leaves[extra], nil))
	newLeaves := append([]common.Hash(nil), leaves[:extra]...)
	newLeaves = append(newLeaves, replacement.Hash())
	siblings, err = ProveHashes(newLeaves, uint64(extra))
	assert.Nil(t, err)
	assert.Nil(t, lc.verifyCanonicalHeader(replacement, siblings))
}

func TestDeepReorgIsRejected(t *testing.T) {
	lc, dispose := buildVerifiedChain(t, 6)
	defer dispose()
	assert.Nil(t, lc.pruneTo(3))
	err := lc.mmr.restore(common.ScdoForkHeight + 1)
	assert.Equal(t, true, err != nil)
	err = lc.RewindVerified(common.ScdoForkHeight + 1)
	assert.Equal(t, true, err != nil)
	assert.Equal(t, uint64(7), lc.MMRLeaves())
}

func TestReverseRefusesPrunedAncestor(t *testing.T) {
	lc, dispose := buildVerifiedChain(t, 6)
	defer dispose()
	assert.Nil(t, lc.pruneTo(3))
	d := newDownloader(lc)
	err := d.reverseLightBCstore(common.ScdoForkHeight + 1)
	assert.Equal(t, true, err != nil)
	assert.Equal(t, uint64(common.ScdoForkHeight)+6, lc.CurrentHeader().Height)
}

func TestHistoricalTxUsesPhoneAccumulator(t *testing.T) {
	lc, dispose := buildVerifiedChain(t, 4)
	defer dispose()
	from, _ := crypto.MustGenerateShardKeyPair(1)
	to, _ := crypto.MustGenerateShardKeyPair(1)
	tx, err := types.NewTransaction(*from, *to, big.NewInt(1), big.NewInt(1), 1)
	assert.Nil(t, err)
	tx.Hash = tx.CalculateHash()
	txTrie := types.GetTxTrie([]*types.Transaction{tx})
	raw, err := txTrie.GetProof(tx.Hash.Bytes())
	assert.Nil(t, err)
	nodes := encodeProof(mapToArray(raw))

	header := lc.GetHeaderByHeight(common.ScdoForkHeight + 2)
	header.TxHash = txTrie.Hash()
	// The committed leaf is the hash of the header as stored. Put the tx root
	// on a copy used only as the proof subject by rebuilding that one leaf.
	updated := *header
	updated.TxHash = txTrie.Hash()
	leaves := make([]common.Hash, 5)
	for i := 0; i < 5; i++ {
		h := lc.GetHeaderByHeight(common.ScdoForkHeight + uint64(i))
		if i == 2 {
			leaves[i] = updated.Hash()
		} else {
			leaves[i] = h.Hash()
		}
	}
	var acc mmr
	for _, leaf := range leaves {
		assert.Nil(t, acc.append(leaf))
	}
	lc.mmr.mmr = acc
	siblings, err := ProveHashes(leaves, 2)
	assert.Nil(t, err)
	assert.Nil(t, lc.verifyCanonicalHeader(&updated, siblings))
	value, err := VerifyTrieValue(updated.TxHash, tx.Hash.Bytes(), nodes)
	assert.Nil(t, err)
	assert.Equal(t, true, len(value) > 0)
}

func TestSnapshotRoundTrip(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	acc, err := loadChainMMR(db)
	assert.Nil(t, err)
	assert.Nil(t, acc.commit(leafHash(1), common.ScdoForkHeight))
	assert.Nil(t, acc.commit(leafHash(2), common.ScdoForkHeight+1))
	snap := acc.clone()
	assert.Nil(t, acc.commit(leafHash(3), common.ScdoForkHeight+2))
	assert.Nil(t, acc.restore(common.ScdoForkHeight+1))
	assert.Equal(t, snap.count, acc.count)
	assert.Nil(t, acc.commit(leafHash(4), common.ScdoForkHeight+2))
	siblings, err := ProveHashes([]common.Hash{leafHash(1), leafHash(2), leafHash(4)}, 2)
	assert.Nil(t, err)
	assert.Equal(t, true, acc.verify(2, leafHash(4), siblings))
	siblings, err = ProveHashes([]common.Hash{leafHash(1), leafHash(2), leafHash(3)}, 2)
	assert.Nil(t, err)
	assert.Equal(t, false, acc.verify(2, leafHash(3), siblings))
}

func TestHeaderProofMatchesPhoneAccumulator(t *testing.T) {
	lc, dispose := buildVerifiedChain(t, 4)
	defer dispose()
	api := &headerProofAPI{chain: lc}
	proof, err := api.GetHeaderProof(common.ScdoForkHeight+1, lc.CurrentHeader().Height)
	assert.Nil(t, err)
	assert.Nil(t, lc.verifyCanonicalHeader(mustHeader(t, proof.Header), mustSiblings(t, proof.Siblings)))
	_, err = api.GetHeaderProof(common.ScdoForkHeight+1, common.ScdoForkHeight)
	assert.Equal(t, true, err != nil)
}

func mustHeader(t *testing.T, hex string) *types.BlockHeader {
	t.Helper()
	header, err := headerFromHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return header
}

func mustSiblings(t *testing.T, hexes []string) []common.Hash {
	t.Helper()
	siblings, err := decodeHashes(hexes)
	if err != nil {
		t.Fatal(err)
	}
	return siblings
}

func TestSyncGatePolicy(t *testing.T) {
	Resume()
	SetSyncPolicy(false, false)
	SetDeviceState(false, false)
	t.Cleanup(func() {
		Resume()
		SetSyncPolicy(false, false)
		SetDeviceState(false, false)
	})
	paused, _ := SyncPause()
	assert.Equal(t, false, paused)
	Pause("user")
	paused, reason := SyncPause()
	assert.Equal(t, true, paused)
	assert.Equal(t, "user", reason)
	SetSyncPolicy(true, true)
	SetDeviceState(true, true)
	Resume()
	paused, reason = SyncPause()
	assert.Equal(t, true, paused)
	assert.Equal(t, "low-battery", reason)
	SetDeviceState(true, false)
	paused, reason = SyncPause()
	assert.Equal(t, true, paused)
	assert.Equal(t, "metered", reason)
	SetDeviceState(false, false)
	paused, _ = SyncPause()
	assert.Equal(t, false, paused)
	assert.Equal(t, false, shouldRetrySession(true, 10, 20, true))
	assert.Equal(t, true, shouldRetrySession(true, 10, 20, false))
	assert.Equal(t, false, shouldRetrySession(false, 10, 20, false))
	assert.Equal(t, false, shouldRetrySession(true, 20, 20, false))
}
