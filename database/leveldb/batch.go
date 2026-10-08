/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"sync/atomic"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// asyncWrite commits a batch without fsync. Combined with Options.NoSync this
// is one write of the whole block (state, receipts, header) instead of a
// disk sync per key. Block import must keep using this.
var asyncWrite = &opt.WriteOptions{Sync: false}

// checkpointWrite is the only batch path that asks LevelDB to sync.
// Nothing on the block-import path calls it. Options.NoSync still suppresses
// the journal fsync while that profile is on.
var checkpointWrite = &opt.WriteOptions{Sync: true}

// batchCommits counts Commit calls. batchSyncCommits counts checkpoint commits.
// Import must leave batchSyncCommits unchanged.
var (
	batchCommits     uint64
	batchSyncCommits uint64
)

// Batch implements batch for leveldb
type Batch struct {
	leveldb *leveldb.DB
	batch   *leveldb.Batch
}

// Put sets the value for the given key
func (b *Batch) Put(key []byte, value []byte) {
	b.batch.Put(key, value)
}

// Delete deletes the value for the given key.
func (b *Batch) Delete(key []byte) {
	b.batch.Delete(key)
}

// Commit commits a batch without fsync. This is the import and block path.
func (b *Batch) Commit() error {
	atomic.AddUint64(&batchCommits, 1)
	return b.leveldb.Write(b.batch, asyncWrite)
}

// CommitCheckpoint commits a batch with WriteOptions.Sync set.
// Call it from an explicit checkpoint, never from per-block import.
func (b *Batch) CommitCheckpoint() error {
	atomic.AddUint64(&batchSyncCommits, 1)
	return b.leveldb.Write(b.batch, checkpointWrite)
}

// Rollback rollbacks batch operation.
func (b *Batch) Rollback() {
	b.batch.Reset()
}
