/**
* @file
* @copyright defined in scdo/LICENSE
 */

package core

import (
	"encoding/json"
	"io/ioutil"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/consensus/pow"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database"
	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/stretchr/testify/assert"
)

func assertSameRecovery(t *testing.T, got, want recoveryPoint) {
	t.Helper()
	got.disk = nil
	want.disk = nil
	assert.Equal(t, want, got)
}

// withImmediateRecovery makes every marker change hit disk. Production import
// waits recoveryFlushInterval and does not fsync.
func withImmediateRecovery(t *testing.T) {
	t.Helper()
	prev := recoveryFlushInterval
	recoveryFlushInterval = 0
	t.Cleanup(func() { recoveryFlushInterval = prev })
}

func newTestRecoveryPointFile() (string, func()) {
	dir, err := ioutil.TempDir("", "SeeleCoreRecoveryPoint")
	if err != nil {
		panic(err)
	}

	return filepath.Join(dir, "rp.bin"), func() {
		os.RemoveAll(dir)
	}
}

func newTestRecoverableBlockchain(bcStore store.BlockchainStore, stateDB database.Database, rpFile string) *Blockchain {
	genesis := newTestGenesis()
	if err := genesis.InitializeAndValidate(bcStore, stateDB); err != nil {
		panic(err)
	}

	bc, err := NewBlockchain(bcStore, stateDB, rpFile, pow.NewEngine(1), nil, -1)
	if err != nil {
		panic(err)
	}

	return bc
}

func Test_RecoveryPoint_FileNotSet(t *testing.T) {
	rp, err := loadRecoveryPoint("")
	assert.Equal(t, err, nil)
	assertSameRecovery(t, *rp, recoveryPoint{})
}

func Test_RecoveryPoint_FileSet(t *testing.T) {
	rpFile, dispose := newTestRecoveryPointFile()
	defer dispose()

	rp, err := loadRecoveryPoint(rpFile)
	assert.Equal(t, err, nil)
	assertSameRecovery(t, *rp, recoveryPoint{file: rpFile})
}

func Test_RecoveryPoint_Serialization(t *testing.T) {
	withImmediateRecovery(t)
	rpFile, dispose := newTestRecoveryPointFile()
	defer dispose()
	rp, _ := loadRecoveryPoint(rpFile)

	// before put block
	rp.WritingBlockHash = common.StringToHash("new block hash")
	rp.WritingBlockHeight = 5
	rp.PreviousHeadBlockHash = common.StringToHash("old HEAD block hash")
	rp.PreviousCanonicalBlockHash = common.StringToHash("old canonical block hash")
	rp.LargerHeight = 6
	rp.StaleHash = common.StringToHash("stale block hash")
	rp.serialize()

	rp2, _ := loadRecoveryPoint(rpFile)
	assertSameRecovery(t, *rp, *rp2)

	// after put block
	rp.onPutBlockEnd()
	rp2, _ = loadRecoveryPoint(rpFile)
	assertSameRecovery(t, *rp2, recoveryPoint{LargerHeight: 6, StaleHash: common.StringToHash("stale block hash"), file: rpFile})

	//delete larger height blocks
	rp.onDeleteLargerHeightBlocks(9)
	rp2, _ = loadRecoveryPoint(rpFile)
	assertSameRecovery(t, *rp2, recoveryPoint{LargerHeight: 9, StaleHash: common.StringToHash("stale block hash"), file: rpFile})

	// overwrite stale blocks in canonical chain
	rp.onDeleteLargerHeightBlocks(0)
	rp.onOverwriteStaleBlocks(common.StringToHash("stale block hash 2"))
	rp2, _ = loadRecoveryPoint(rpFile)
	assertSameRecovery(t, *rp2, recoveryPoint{StaleHash: common.StringToHash("stale block hash 2"), file: rpFile})
}

func Test_RecoveryPoint_AtomicWrite(t *testing.T) {
	rpFile, dispose := newTestRecoveryPointFile()
	defer dispose()

	original := []byte("{\"LargerHeight\":1}\n")
	assert.Nil(t, ioutil.WriteFile(rpFile, original, 0644))

	rp, err := loadRecoveryPoint(rpFile)
	assert.Nil(t, err)
	rp.LargerHeight = 9
	rp.StaleHash = common.StringToHash("stale block hash")
	rp.serialize()

	if _, err := os.Stat(rpFile + ".tmp"); err == nil {
		t.Fatal("temp recovery file was left behind")
	}
	loaded, err := loadRecoveryPoint(rpFile)
	assert.Nil(t, err)
	assert.Equal(t, uint64(9), loaded.LargerHeight)
	assert.Equal(t, rp.StaleHash, loaded.StaleHash)

	// A failed replace must leave the previous file readable.
	assert.NotNil(t, writeAtomic(rpFile+"/missing", []byte("{}"), false))
	again, err := ioutil.ReadFile(rpFile)
	assert.Nil(t, err)
	assert.True(t, len(again) > 0)
	var parsed recoveryPoint
	assert.Nil(t, json.Unmarshal(again, &parsed))
}

// Test_RecoveryPoint_FsyncsPerImportedBlock is the HDD regression check.
// a0a0423 fsynced recoveryPoint.json on both sides of every block write
// (about 6 flush requests per block on a 4-shard node). Import may replace
// the file at most once per interval and must not fsync. Shutdown fsyncs once.
func Test_RecoveryPoint_FsyncsPerImportedBlock(t *testing.T) {
	prevInterval := recoveryFlushInterval
	prevNow := recoveryNow
	recoveryFlushInterval = 10 * time.Second
	clock := time.Unix(1_700_000_000, 0)
	recoveryNow = func() time.Time { return clock }
	t.Cleanup(func() {
		recoveryFlushInterval = prevInterval
		recoveryNow = prevNow
	})

	rpFile, dispose := newTestRecoveryPointFile()
	defer dispose()
	rp, err := loadRecoveryPoint(rpFile)
	assert.Nil(t, err)

	const blocks = 200
	writesBefore := atomic.LoadInt64(&recoveryDiskWrites)
	syncsBefore := atomic.LoadInt64(&recoveryFsyncs)
	for i := 0; i < blocks; i++ {
		var hash common.Hash
		hash[0] = byte(i)
		hash[1] = byte(i >> 8)
		rp.WritingBlockHash = hash
		rp.WritingBlockHeight = uint64(i + 1)
		rp.PreviousHeadBlockHash = common.StringToHash("head")
		rp.serialize()
		rp.onPutBlockEnd()
	}

	writes := atomic.LoadInt64(&recoveryDiskWrites) - writesBefore
	syncs := atomic.LoadInt64(&recoveryFsyncs) - syncsBefore
	t.Logf("fsyncs per imported block during sync: %.4f (atomic renames %d / %d blocks)", float64(syncs)/float64(blocks), writes, blocks)
	if writes > 1 {
		t.Fatalf("recoveryPoint rewrote %d times inside one interval, want at most 1", writes)
	}
	if syncs != 0 {
		t.Fatalf("hot path fsynced %d times (%.4f per block), want 0", syncs, float64(syncs)/float64(blocks))
	}
	if rp.WritingBlockHeight != 0 || !rp.WritingBlockHash.IsEmpty() {
		t.Fatal("in-memory marker was not updated when the disk write was skipped")
	}

	clock = clock.Add(11 * time.Second)
	rp.LargerHeight = 42
	rp.serialize()
	writes = atomic.LoadInt64(&recoveryDiskWrites) - writesBefore
	syncs = atomic.LoadInt64(&recoveryFsyncs) - syncsBefore
	if writes != 2 {
		t.Fatalf("crossing the interval rewrote the file %d times, want 2", writes)
	}
	if syncs != 0 {
		t.Fatal("interval flush fsynced; want an atomic rename only")
	}

	rp.flush()
	writes = atomic.LoadInt64(&recoveryDiskWrites) - writesBefore
	syncs = atomic.LoadInt64(&recoveryFsyncs) - syncsBefore
	if syncs != 1 {
		t.Fatalf("clean shutdown fsynced %d times, want 1", syncs)
	}
	if writes != 3 {
		t.Fatalf("shutdown write count %d, want 3", writes)
	}
	loaded, err := loadRecoveryPoint(rpFile)
	assert.Nil(t, err)
	assert.Equal(t, uint64(42), loaded.LargerHeight)
	t.Logf("fsyncs per imported block including one shutdown flush: %.4f", float64(syncs)/float64(blocks))
}

func BenchmarkRecoveryPointPerImportedBlock(b *testing.B) {
	prevInterval := recoveryFlushInterval
	recoveryFlushInterval = 10 * time.Second
	b.Cleanup(func() { recoveryFlushInterval = prevInterval })

	dir, err := ioutil.TempDir("", "rp-bench")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	rp, err := loadRecoveryPoint(filepath.Join(dir, "recoveryPoint.json"))
	if err != nil {
		b.Fatal(err)
	}

	syncsBefore := atomic.LoadInt64(&recoveryFsyncs)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rp.WritingBlockHeight = uint64(i + 1)
		rp.serialize()
		rp.onPutBlockEnd()
	}
	b.StopTimer()
	syncs := atomic.LoadInt64(&recoveryFsyncs) - syncsBefore
	if b.N > 0 {
		b.ReportMetric(float64(syncs)/float64(b.N), "fsyncs/block")
	}
}

func Test_RecoveryPoint_PutBlockCorrupted(t *testing.T) {
	withImmediateRecovery(t)
	rpFile, dispose1 := newTestRecoveryPointFile()
	defer dispose1()

	db, dispose2 := leveldb.NewTestDatabase()
	defer dispose2()

	// mock corrupt when put a block in DB.
	bcStore := store.NewMemStore()
	bcStore.CorruptOnPutBlock = true

	// should fail to write block due to DB corruption
	// and the inserted block exists in DB
	bc := newTestRecoverableBlockchain(bcStore, db, rpFile)
	newBlock := newTestBlock(bc, bc.genesisBlock.HeaderHash, 1, 3, 0)
	assert.True(t, errors.IsOrContains(bc.WriteBlock(newBlock, nil), store.ErrDBCorrupt))

	// the inserted block exists in DB after corruption
	_, err := bcStore.GetBlock(newBlock.HeaderHash)
	assert.Equal(t, err, nil)

	// the previous inserted block should not exist in DB anymore after recover
	newTestRecoverableBlockchain(bcStore, db, rpFile)
	if _, err = bcStore.GetBlock(newBlock.HeaderHash); err == nil {
		t.Fatal()
	}
}

func Test_RecoveryPoint_RecoverDeleteLargerHeightBlocks(t *testing.T) {
	// height 7 block not deleted before corruption
	rp := recoveryPoint{LargerHeight: 7}
	bcStore := store.NewMemStore()
	block7 := newTestRPBlock(common.StringToHash("block 7"), 7)
	bcStore.PutBlock(block7, big.NewInt(7), true)
	block8 := newTestRPBlock(common.StringToHash("block 8"), 8)
	bcStore.PutBlock(block8, big.NewInt(8), true)

	assert.Equal(t, rp.recover(bcStore), nil)

	if _, err := bcStore.GetBlockHash(7); err == nil {
		t.Fatal()
	}

	if _, err := bcStore.GetBlockHash(8); err == nil {
		t.Fatal()
	}

	// height 7 block already deleted before corruption
	rp = recoveryPoint{LargerHeight: 7}
	bcStore = store.NewMemStore()
	bcStore.PutBlock(block8, big.NewInt(8), true)

	assert.Equal(t, rp.recover(bcStore), nil)

	if _, err := bcStore.GetBlockHash(8); err == nil {
		t.Fatal()
	}
}

func newTestRPBlock(preBlockHash common.Hash, height uint64) *types.Block {
	header := &types.BlockHeader{
		PreviousBlockHash: preBlockHash,
		Height:            height,
	}

	return &types.Block{
		Header:     header,
		HeaderHash: header.Hash(),
	}
}

func Test_RecoveryPoint_RecoverOverwriteStaleBlocks(t *testing.T) {
	bcStore := store.NewMemStore()

	block3 := newTestRPBlock(common.StringToHash("block 2"), 3)
	bcStore.PutBlock(block3, big.NewInt(3), true)

	// old canonical chain
	block41 := newTestRPBlock(block3.HeaderHash, 4)
	bcStore.PutBlock(block41, big.NewInt(4), true)
	block51 := newTestRPBlock(block41.HeaderHash, 5)
	bcStore.PutBlock(block51, big.NewInt(5), true)

	// new canonical chain
	block42 := newTestRPBlock(block3.HeaderHash, 4)
	bcStore.PutBlock(block42, big.NewInt(4), false)
	block52 := newTestRPBlock(block42.HeaderHash, 5)
	bcStore.PutBlock(block52, big.NewInt(5), false)

	// recover: overwrite stale blocks from block52
	// the common ancester is block3, so height 4 and 5
	// in canonical chain will be overwritten.
	rp := recoveryPoint{StaleHash: block52.HeaderHash}
	assert.Equal(t, rp.recover(bcStore), nil)

	hash, err := bcStore.GetBlockHash(5)
	assert.Equal(t, err, nil)
	assert.Equal(t, hash, block52.HeaderHash)

	hash, err = bcStore.GetBlockHash(4)
	assert.Equal(t, err, nil)
	assert.Equal(t, hash, block42.HeaderHash)
}
