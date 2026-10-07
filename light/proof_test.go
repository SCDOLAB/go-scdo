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
	"github.com/scdoproject/go-scdo/trie"
	"github.com/stretchr/testify/assert"
)

func TestGetStateWithoutPeersDoesNotPanic(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	bcStore := store.NewBlockchainDatabase(db)
	header := &types.BlockHeader{
		Creator:         common.EmptyAddress,
		Difficulty:      big.NewInt(1),
		Height:          1,
		CreateTimestamp: big.NewInt(1),
		StateHash:       common.StringToHash("StateHash"),
		TxHash:          common.StringToHash("TxHash"),
	}
	if err := bcStore.PutBlockHeader(header.Hash(), header, header.Difficulty, true); err != nil {
		t.Fatal(err)
	}
	chain, err := newLightChain(bcStore, db, newOdrBackend(bcStore, 1), pow.NewEngine(1))
	if err != nil {
		t.Fatal(err)
	}
	statedb, err := chain.GetState(header.StateHash)
	if err != nil {
		t.Fatal(err)
	}
	_ = statedb.GetBalance(common.EmptyAddress)
	if statedb.GetDbErr() == nil {
		t.Fatal("expected an error when no light peer is connected")
	}
}

func TestAccountProofRoundTrip(t *testing.T) {
	addr, _ := crypto.MustGenerateShardKeyPair(1)
	key := accountTrieKey(*addr)
	leaf, err := common.Serialize(provenAccount{Nonce: 4, Amount: big.NewInt(99), CodeHash: []byte{1}})
	assert.Nil(t, err)

	db := trie.NewEmptyTrie(nil, nil)
	assert.Nil(t, db.Put(key, leaf))
	root := db.Hash()
	raw, err := db.GetProof(key)
	assert.Nil(t, err)

	nodes := encodeProof(mapToArray(raw))
	value, err := VerifyTrieValue(root, key, nodes)
	assert.Nil(t, err)
	amount, nonce, included, err := DecodeAccount(value)
	assert.Nil(t, err)
	assert.Equal(t, true, included)
	assert.Equal(t, uint64(4), nonce)
	assert.Equal(t, 0, amount.Cmp(big.NewInt(99)))
}

func TestTxAndDebtProofRoundTrip(t *testing.T) {
	from, _ := crypto.MustGenerateShardKeyPair(1)
	to, _ := crypto.MustGenerateShardKeyPair(1)
	tx, err := types.NewTransaction(*from, *to, big.NewInt(1), big.NewInt(1), 1)
	assert.Nil(t, err)
	tx.Hash = tx.CalculateHash()
	txTrie := types.GetTxTrie([]*types.Transaction{tx})
	raw, err := txTrie.GetProof(tx.Hash.Bytes())
	assert.Nil(t, err)
	value, err := VerifyTrieValue(txTrie.Hash(), tx.Hash.Bytes(), encodeProof(mapToArray(raw)))
	assert.Nil(t, err)
	got := new(types.Transaction)
	assert.Nil(t, common.Deserialize(value, got))
	assert.Equal(t, tx.Hash, got.Hash)

	debt := &types.Debt{
		Hash: common.BytesToHash([]byte{9}),
		Data: types.DebtData{Amount: big.NewInt(7), Price: big.NewInt(1), TxHash: common.BytesToHash([]byte{8})},
	}
	debtTrie := types.GetDebtTrie([]*types.Debt{debt})
	raw, err = debtTrie.GetProof(debt.Hash.Bytes())
	assert.Nil(t, err)
	value, err = VerifyTrieValue(debtTrie.Hash(), debt.Hash.Bytes(), encodeProof(mapToArray(raw)))
	assert.Nil(t, err)
	gotDebt := new(types.Debt)
	assert.Nil(t, common.Deserialize(value, gotDebt))
	assert.Equal(t, debt.Hash, gotDebt.Hash)
}

func TestConfirmationsUseExistingRule(t *testing.T) {
	have, ok := confirmations(200, 80)
	assert.Equal(t, uint64(120), have)
	assert.Equal(t, true, ok)
	have, ok = confirmations(200, 81)
	assert.Equal(t, uint64(119), have)
	assert.Equal(t, false, ok)
}

func TestPhoneEstimateFitsALargePhone(t *testing.T) {
	header := sampleClassicHeader()
	raw := common.SerializePanic(header)
	est := EstimateSync(SampledClassicHead, len(raw), HeaderDiskBytes(header, header.Difficulty))
	assert.Equal(t, uint64(common.ScdoForkHeight), est.ForkGenesis)
	assert.Equal(t, 4, est.Shards)
	assert.Equal(t, true, est.HeadersPerShard > uint64(6_000_000))
	// One header is a few hundred bytes. Four shards from fork genesis are
	// several gigabytes, which is a phone with room to spare, not the full chain.
	assert.Equal(t, true, est.BytesPerHeader < 2000)
	assert.Equal(t, true, est.PhoneStorageBytes > uint64(4<<30))
	assert.Equal(t, true, est.PhoneStorageBytes < uint64(20<<30))
	assert.Equal(t, true, est.SteadyBytesPerSecond < 500)
	t.Logf("header %d bytes, disk %d bytes, phone storage %d, download %d, steady %.1f B/s",
		len(raw), est.BytesPerHeader, est.PhoneStorageBytes, est.DownloadBytes, est.SteadyBytesPerSecond)
}
