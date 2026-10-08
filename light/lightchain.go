/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"math/big"
	"sync"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/consensus"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/core/state"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database"
	"github.com/scdoproject/go-scdo/event"
	"github.com/scdoproject/go-scdo/log"
)

// LightChain represents a canonical chain that by default only handles block headers.
type LightChain struct {
	mutex                     sync.RWMutex
	bcStore                   store.BlockchainStore
	db                        database.Database
	mmr                       *chainMMR
	odrBackend                *odrBackend
	engine                    consensus.Engine
	currentHeader             *types.BlockHeader
	canonicalTD               *big.Int
	headerChangedEventManager *event.EventManager
	headRollbackEventManager  *event.EventManager
	log                       *log.ScdoLog
	// retainAll keeps every verified header. Full nodes need other-shard
	// headers back to fork genesis so debt checks can see the heights their
	// own chain is still validating. Phones in lite mode leave this false
	// and keep only the recent window.
	retainAll bool
}

// newLightChain create light chain. Phone lite mode prunes to the retained window.
func newLightChain(bcStore store.BlockchainStore, lightDB database.Database, odrBackend *odrBackend, engine consensus.Engine) (*LightChain, error) {
	return openLightChain(bcStore, lightDB, odrBackend, engine, false)
}

// openLightChain create light chain. retainAll is set before the accumulator
// runs so a full node neither prunes nor refuses to rebuild a pruned store.
func openLightChain(bcStore store.BlockchainStore, lightDB database.Database, odrBackend *odrBackend, engine consensus.Engine, retainAll bool) (*LightChain, error) {
	chain := &LightChain{
		bcStore:                   bcStore,
		db:                        lightDB,
		odrBackend:                odrBackend,
		engine:                    engine,
		headerChangedEventManager: event.NewEventManager(),
		headRollbackEventManager:  event.NewEventManager(),
		log:                       log.GetLogger("LightChain"),
		retainAll:                 retainAll,
	}

	currentHeaderHash, err := bcStore.GetHeadBlockHash()
	if err != nil {
		return nil, errors.NewStackedError(err, "failed to get HEAD block hash")
	}

	chain.currentHeader, err = bcStore.GetBlockHeader(currentHeaderHash)
	if err != nil {
		return nil, errors.NewStackedErrorf(err, "failed to get block header, hash = %v", currentHeaderHash)
	}

	td, err := bcStore.GetBlockTotalDifficulty(currentHeaderHash)
	if err != nil {
		return nil, errors.NewStackedErrorf(err, "failed to get block TD, hash = %v", currentHeaderHash)
	}

	chain.canonicalTD = td
	if err = chain.prepareAccumulator(); err != nil {
		return nil, errors.NewStackedError(err, "failed to prepare the header accumulator")
	}

	return chain, nil
}

// GetState returns a statedb that reads accounts through a Merkle proof for root.
// root is the state root of a header this node already checked. The proof is
// verified against that root. A full node serves the proof; this client does
// not store the account trie.
func (lc *LightChain) GetState(root common.Hash) (*state.Statedb, error) {
	blockHash := common.EmptyHash
	if lc.currentHeader != nil {
		stateHash := lc.currentHeader.StateHash
		if stateHash.Equal(root) {
			blockHash = lc.currentHeader.Hash()
		}
	}
	return lc.GetStateByRootAndBlockHash(root, blockHash)
}

// GetStateByRootAndBlockHash get the statedb by root and block hash
func (lc *LightChain) GetStateByRootAndBlockHash(root, blockHash common.Hash) (*state.Statedb, error) {
	trie := newOdrTrie(lc.odrBackend, root, state.TrieDbPrefix, blockHash)
	return state.NewStatedbWithTrie(trie), nil
}

// CurrentHeader returns the HEAD block header of the blockchain.
func (lc *LightChain) CurrentHeader() *types.BlockHeader {
	return lc.currentHeader
}

// GetStore get underlying store
func (lc *LightChain) GetStore() store.BlockchainStore {
	return lc.bcStore
}

// GetHeader retrieves a block header from the database by height.
func (lc *LightChain) GetHeaderByHeight(height uint64) *types.BlockHeader {
	hash, err := lc.bcStore.GetBlockHash(height)
	if err != nil {
		lc.log.Warn("get block header by height failed, err %s. height %d", err, height)
		return nil
	}

	return lc.GetHeaderByHash(hash)
}

// GetHeaderByNumber retrieves a block header from the database by hash.
func (lc *LightChain) GetHeaderByHash(hash common.Hash) *types.BlockHeader {
	header, err := lc.bcStore.GetBlockHeader(hash)
	if err != nil {
		lc.log.Debug("get block header by hash failed, err %s, hash: %v", err, hash)
		return nil
	}

	return header
}

// GetHeaderByHash
func (lc *LightChain) GetBlockByHash(hash common.Hash) *types.Block {
	// this is only provided for miner interface. for light chain, there is no mining, so just return nil.
	return nil
}

// WriteHeader writes the specified block header to the blockchain.
func (lc *LightChain) WriteHeader(header *types.BlockHeader) error {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	if err := core.ValidateBlockHeader(header, lc.engine, lc.bcStore, lc); err != nil {
		return errors.NewStackedError(err, "failed to validate block header")
	}

	previousTd, err := lc.bcStore.GetBlockTotalDifficulty(header.PreviousBlockHash)
	if err != nil {
		return errors.NewStackedErrorf(err, "failed to get block TD, hash = %v", header.PreviousBlockHash)
	}

	currentTd := new(big.Int).Add(previousTd, header.Difficulty)
	isHead := currentTd.Cmp(lc.canonicalTD) > 0

	// Record the hash only after parent, difficulty and ZPoW checks succeed,
	// and before the header becomes canonical. A deep reorg that the
	// accumulator cannot represent is refused without moving the tip.
	if isHead {
		if err = lc.noteVerified(header); err != nil {
			return errors.NewStackedError(err, "failed to record verified header")
		}
	}

	// Header rows have no body. The full-chain stale-block walk looks one
	// header below the parent and aborts when that header was pruned, which
	// stalled sync with "failed to overwrite stale blocks in old canonical
	// chain". Update the height index directly and stop at the first gap.
	if isHead {
		if err = lc.retargetCanonical(header); err != nil {
			return err
		}
		if err = lc.dropCanonicalFrom(header.Height); err != nil {
			return errors.NewStackedErrorf(err, "failed to clear canonical headers from height %d", header.Height)
		}
	}

	if err := lc.bcStore.PutBlockHeader(header.Hash(), header, currentTd, isHead); err != nil {
		return errors.NewStackedErrorf(err, "failed to put block header, header = %+v", header)
	}

	if !isHead {
		return nil
	}

	lc.canonicalTD = currentTd
	lc.currentHeader = header

	lc.headerChangedEventManager.Fire(header)

	return nil
}

// GetCurrentState get current state
func (lc *LightChain) GetCurrentState() (*state.Statedb, error) {
	return lc.GetStateByRootAndBlockHash(lc.currentHeader.StateHash, lc.currentHeader.Hash())
}

// GetHeadRollbackEventManager
func (lc *LightChain) GetHeadRollbackEventManager() *event.EventManager {
	return lc.headRollbackEventManager
}

// PutTd set light chain canonial total difficulty
func (lc *LightChain) PutTd(td *big.Int) {
	lc.canonicalTD = td
}

// PutCurrentHeader
func (lc *LightChain) PutCurrentHeader(header *types.BlockHeader) {
	lc.currentHeader = header
}
