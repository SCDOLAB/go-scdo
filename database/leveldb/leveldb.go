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
	"github.com/syndtr/goleveldb/leveldb/opt"
)

const (
	// InitialSyncCacheMB is the chain cache while the database is still small.
	InitialSyncCacheMB = 512
	// SteadyCacheMB is the chain cache after the database has grown.
	SteadyCacheMB = 128
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

// NewLevelDB constructs and returns a LevelDB instance with the automatic cache size.
func NewLevelDB(path string) (database.Database, error) {
	return NewLevelDBWithCache(path, 0)
}

// NewLevelDBWithCache opens LevelDB with cacheMB megabytes of cache.
// cacheMB <= 0 uses AutoCacheMB. Block writes already commit as one LevelDB
// batch per block; the larger write buffer coalesces those batches during sync.
func NewLevelDBWithCache(path string, cacheMB int) (database.Database, error) {
	if cacheMB <= 0 {
		cacheMB = AutoCacheMB(path)
	}
	opts := cacheOptions(cacheMB)
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
	if cacheMB < 16 {
		cacheMB = 16
	}
	bytes := cacheMB * opt.MiB
	writeBuffer := bytes / 4
	if writeBuffer < 4*opt.MiB {
		writeBuffer = 4 * opt.MiB
	}
	if writeBuffer > 128*opt.MiB {
		writeBuffer = 128 * opt.MiB
	}
	return &opt.Options{
		BlockCacheCapacity:  bytes / 2,
		WriteBuffer:         writeBuffer,
		CompactionTableSize: 8 * opt.MiB,
		CompactionL0Trigger: 8,
	}
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
