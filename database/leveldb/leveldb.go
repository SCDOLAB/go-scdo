/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/scdoproject/go-scdo/database"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"github.com/syndtr/goleveldb/leveldb/filter"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

const (
	// InitialSyncCacheMB is the block cache for a new database.
	// 64 MiB is the phone and HDD profile. 512 MiB per database does not
	// fit four Classic shards on a phone (three databases each).
	InitialSyncCacheMB = 64
	// SteadyCacheMB is the same profile once the database has grown.
	// The cache no longer jumps to 512 MiB during the initial sync.
	SteadyCacheMB = 64
	// initialSyncBytes treats a chain directory under this size as an initial sync.
	initialSyncBytes = 64 << 20
)

var (
	// ErrEmptyKey key is empty
	ErrEmptyKey = errors.New("key could not be empty")
)

// LevelDB wraps the leveldb
type LevelDB struct {
	db       *leveldb.DB
	quitChan chan struct{} // used by metrics
}

// AutoCacheMB returns InitialSyncCacheMB for a new or small database and
// SteadyCacheMB once the directory has grown past the initial sync.
func AutoCacheMB(path string) int {
	if directoryBytes(path) < initialSyncBytes {
		return InitialSyncCacheMB
	}
	return SteadyCacheMB
}

// syncOptions is the LevelDB profile used while a node writes the chain.
// Defaults (4MiB buffer, 2MiB tables, 8MiB cache, no bloom filter, fsync on
// every new table) stall a slow disk once compaction starts. These values
// keep block writes in larger buffers, skip per-table fsync, and skip
// negative bloom lookups instead of random reads.
func syncOptions() *opt.Options {
	return optionsWithCache(InitialSyncCacheMB, 32)
}

func optionsWithCache(blockCacheMB, writeBufferMB int) *opt.Options {
	if blockCacheMB < 8 {
		blockCacheMB = 8
	}
	if writeBufferMB < 4 {
		writeBufferMB = 4
	}
	if writeBufferMB > 32 {
		writeBufferMB = 32
	}
	return &opt.Options{
		BlockCacheCapacity:     blockCacheMB * opt.MiB,
		WriteBuffer:            writeBufferMB * opt.MiB,
		CompactionTableSize:    8 * opt.MiB,
		CompactionTotalSize:    64 * opt.MiB,
		CompactionL0Trigger:    8,
		WriteL0SlowdownTrigger: 16,
		WriteL0PauseTrigger:    24,
		OpenFilesCacheCapacity: 1024,
		Filter:                 filter.NewBloomFilter(10),
		// Journal and table fsync is skipped. A process crash still has the
		// OS page cache; a power loss can drop the tail, which the recovery
		// point rebuilds. Per-block fsync is what drops a slow disk under 50 blk/s.
		NoSync: true,
	}
}

// NewLevelDB constructs and returns a LevelDB instance with the phone/HDD profile.
func NewLevelDB(path string) (database.Database, error) {
	return openLevelDB(path, syncOptions())
}

// NewLevelDBWithCache opens LevelDB with cacheMB megabytes of block cache.
// cacheMB <= 0 uses the 64 MiB phone/HDD profile. Bloom filters, 8 MiB
// tables and NoSync stay on for every size so a phone and a desktop share
// the same write path.
func NewLevelDBWithCache(path string, cacheMB int) (database.Database, error) {
	if cacheMB <= 0 {
		cacheMB = AutoCacheMB(path)
	}
	return openLevelDB(path, cacheOptions(cacheMB))
}

func openLevelDB(path string, opts *opt.Options) (database.Database, error) {
	db, err := leveldb.OpenFile(path, opts)

	if _, corrupted := err.(*errors.ErrCorrupted); corrupted {
		db, err = leveldb.RecoverFile(path, opts)
	}

	if err != nil {
		return nil, err
	}

	result := &LevelDB{
		db:       db,
		quitChan: make(chan struct{}),
	}

	return result, nil
}

func cacheOptions(cacheMB int) *opt.Options {
	if cacheMB <= 0 {
		return syncOptions()
	}
	writeBufferMB := cacheMB / 2
	if writeBufferMB < 4 {
		writeBufferMB = 4
	}
	if writeBufferMB > 32 {
		writeBufferMB = 32
	}
	return optionsWithCache(cacheMB, writeBufferMB)
}

func directoryBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return 0
	}
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		if total >= initialSyncBytes {
			return stopWalk
		}
		return nil
	})
	return total
}

type stopWalkError struct{}

func (stopWalkError) Error() string { return "stop walk" }

var stopWalk = stopWalkError{}

// Close is used to close the db when not used
func (db *LevelDB) Close() {
	close(db.quitChan)
	db.db.Close()
}

// GetString gets the value for the given key
func (db *LevelDB) GetString(key string) (string, error) {
	value, err := db.Get([]byte(key))

	return string(value), err
}

// Get gets the value for the given key
func (db *LevelDB) Get(key []byte) ([]byte, error) {
	return db.db.Get(key, nil)
}

// Put sets the value for the given key
func (db *LevelDB) Put(key []byte, value []byte) error {
	if len(key) < 1 {
		return ErrEmptyKey
	}

	return db.db.Put(key, value, nil)
}

// PutString sets the value for the given key
func (db *LevelDB) PutString(key string, value string) error {
	return db.Put([]byte(key), []byte(value))
}

// Has returns true if the DB does contain the given key.
func (db *LevelDB) Has(key []byte) (ret bool, err error) {
	return db.db.Has(key, nil)
}

// HasString returns true if the DB does contain the given key.
func (db *LevelDB) HasString(key string) (ret bool, err error) {
	return db.Has([]byte(key))
}

// Delete deletes the value for the given key.
func (db *LevelDB) Delete(key []byte) error {
	return db.db.Delete(key, nil)
}

// DeleteSring deletes the value for the given key.
func (db *LevelDB) DeleteSring(key string) error {
	return db.Delete([]byte(key))
}

// NewBatch constructs and returns a batch object
func (db *LevelDB) NewBatch() database.Batch {
	batch := &Batch{
		leveldb: db.db,
		batch:   new(leveldb.Batch),
	}
	return batch
}

// NewTestDatabase creates a database instance under temp folder.
func NewTestDatabase() (db database.Database, dispose func()) {
	dir, err := ioutil.TempDir("", "Scdo-LevelDB-")
	if err != nil {
		panic(err)
	}

	db, err = NewLevelDB(dir)
	if err != nil {
		os.RemoveAll(dir)
		panic(err)
	}

	return db, func() {
		db.Close()
		os.RemoveAll(dir)
	}
}
