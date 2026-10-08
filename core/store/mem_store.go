package store

import (
	"math/big"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database/leveldb"
)

// ErrDBCorrupt is returned by MemStore when CorruptOnPutBlock is set.
var ErrDBCorrupt = errorsNew("database is corrupted")

func errorsNew(msg string) error { return &memError{msg} }

type memError struct{ msg string }

func (e *memError) Error() string { return e.msg }

// MemStore is an in-process blockchain store for tests.
type MemStore struct {
	*blockchainDatabase
	CorruptOnPutBlock bool
}

// NewMemStore returns a temporary blockchain store.
func NewMemStore() *MemStore {
	db, _ := leveldb.NewTestDatabase()
	return &MemStore{blockchainDatabase: &blockchainDatabase{db: db}}
}

// PutBlock writes the block, then returns ErrDBCorrupt when asked to simulate a crash.
func (m *MemStore) PutBlock(block *types.Block, td *big.Int, isHead bool) error {
	err := m.blockchainDatabase.PutBlock(block, td, isHead)
	if err != nil {
		return err
	}
	if m.CorruptOnPutBlock {
		return ErrDBCorrupt
	}
	return nil
}

// PutBlockBundle writes the bundle, then returns ErrDBCorrupt when asked to simulate a crash.
func (m *MemStore) PutBlockBundle(block *types.Block, td *big.Int, isHead bool, receipts []*types.Receipt, accounts []common.Address) error {
	err := m.blockchainDatabase.PutBlockBundle(block, td, isHead, receipts, accounts)
	if err != nil {
		return err
	}
	if m.CorruptOnPutBlock {
		return ErrDBCorrupt
	}
	return nil
}
