/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"fmt"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
)

// RetainedHeaders is how many canonical headers each shard keeps after they
// have been verified. 10_000 blocks is about 55 hours at the 20 second
// target, which covers the 120-confirmation debt rule and ordinary reorgs.
// Fork genesis is kept as well, because the handshake compares it.
const RetainedHeaders = 10000

// noteVerified records a canonical header in the accumulator, then drops the
// header that just fell out of the retained window. Side blocks are not
// recorded. Headers below fork genesis belong to unit tests and are ignored.
func (lc *LightChain) noteVerified(header *types.BlockHeader) error {
	if header == nil || header.Height < common.ScdoForkHeight || lc.db == nil {
		return nil
	}
	if lc.mmr == nil {
		loaded, err := loadChainMMR(lc.db)
		if err != nil {
			return err
		}
		lc.mmr = loaded
	}
	index := header.Height - common.ScdoForkHeight
	if lc.mmr.count == index+1 && lc.mmr.tip == header.Hash() {
		return lc.pruneOne(header.Height)
	}
	if lc.mmr.count != index {
		if header.Height == common.ScdoForkHeight {
			return fmt.Errorf("fork genesis accumulator mismatch")
		}
		if err := lc.mmr.restore(header.Height - 1); err != nil {
			return errors.NewStackedError(err, "reorg is deeper than the retained header window")
		}
		if lc.mmr.count != index {
			return fmt.Errorf("header accumulator does not match parent height %d", header.Height-1)
		}
	}
	if err := lc.mmr.commit(header.Hash(), header.Height); err != nil {
		return err
	}
	return lc.pruneOne(header.Height)
}

func (lc *LightChain) pruneOne(head uint64) error {
	return lc.pruneWindow(head, uint64(RetainedHeaders))
}

// pruneWindow drops the canonical header at head-keep. Genesis stays.
func (lc *LightChain) pruneWindow(head, keep uint64) error {
	if keep == 0 || head <= common.ScdoForkHeight+keep {
		return nil
	}
	return lc.dropHeight(head - keep)
}

func (lc *LightChain) pruneToWindow() error {
	return lc.pruneTo(uint64(RetainedHeaders))
}

// pruneTo brings a chain that still has the full header history, such as an
// upgrade from the unpruned light client, down to the retained window.
// A chain that is already pruned drops only the single height just below
// the window.
func (lc *LightChain) pruneTo(keep uint64) error {
	if lc.currentHeader == nil || keep == 0 {
		return nil
	}
	head := lc.currentHeader.Height
	if head <= common.ScdoForkHeight+keep {
		return nil
	}
	floor := head - keep
	sweep := false
	if _, err := lc.bcStore.GetBlockHash(common.ScdoForkHeight + 1); err == nil && common.ScdoForkHeight+1 <= floor {
		sweep = true
	}
	if !sweep {
		return lc.dropHeight(floor)
	}
	for h := uint64(common.ScdoForkHeight) + 1; h <= floor; h++ {
		if err := lc.dropHeight(h); err != nil {
			return err
		}
		if (h-common.ScdoForkHeight)%100000 == 0 {
			lc.log.Info("pruned verified headers through height %d (keeping the last %d)", h, keep)
		}
	}
	lc.log.Info("header history pruned to the last %d headers plus fork genesis", keep)
	return nil
}

// dropHeight removes one canonical header, its total difficulty, the
// height-to-hash index, and the accumulator snapshot. A missing row is the
// steady state, not an error. Fork genesis is never removed.
func (lc *LightChain) dropHeight(height uint64) error {
	if height <= common.ScdoForkHeight {
		return nil
	}
	hash, err := lc.bcStore.GetBlockHash(height)
	if err != nil {
		if isNotFound(err) {
			if lc.db != nil {
				_ = lc.db.Delete(mmrSnapKey(height))
			}
			return nil
		}
		return err
	}
	// Delete the header while the height mapping still names it, so the
	// cached store drops the hash cache too.
	if err = lc.bcStore.DeleteBlockHeader(hash); err != nil && !isNotFound(err) {
		return err
	}
	if _, err = lc.bcStore.DeleteBlockHash(height); err != nil && !isNotFound(err) {
		return err
	}
	if lc.db != nil {
		if err = lc.db.Delete(mmrSnapKey(height)); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// prepareAccumulator loads the MMR, commits fork genesis on a fresh store,
// finishes a one-header gap, rebuilds from headers that are still on disk,
// and prunes anything outside the window.
func (lc *LightChain) prepareAccumulator() error {
	if lc.db == nil || lc.currentHeader == nil || lc.currentHeader.Height < common.ScdoForkHeight {
		return nil
	}
	loaded, err := loadChainMMR(lc.db)
	if err != nil {
		return err
	}
	lc.mmr = loaded
	if lc.currentHeader.Height == common.ScdoForkHeight && lc.mmr.count == 0 {
		if err = lc.mmr.commit(lc.currentHeader.Hash(), lc.currentHeader.Height); err != nil {
			return err
		}
	}
	if err = lc.repair(); err != nil {
		return err
	}
	return lc.pruneToWindow()
}

func (lc *LightChain) repair() error {
	head := lc.currentHeader.Height
	index := head - common.ScdoForkHeight
	expected := index + 1
	switch {
	case lc.mmr.count == expected && lc.mmr.tip == lc.currentHeader.Hash():
		return nil
	case lc.mmr.count == expected:
		return lc.noteVerified(lc.currentHeader)
	case lc.mmr.count == index:
		return lc.mmr.commit(lc.currentHeader.Hash(), head)
	case lc.mmr.count > expected:
		if err := lc.mmr.restore(head); err != nil {
			return errors.NewStackedError(err, "verified head has no accumulator snapshot")
		}
		if lc.mmr.count != expected {
			return fmt.Errorf("accumulator snapshot at height %d has %d leaves, want %d", head, lc.mmr.count, expected)
		}
		return nil
	default:
		lc.log.Info("rebuilding header accumulator from stored headers %d..%d", common.ScdoForkHeight, head)
		return lc.rebuildMMR()
	}
}

func (lc *LightChain) rebuildMMR() error {
	head := lc.currentHeader.Height
	fresh := &chainMMR{db: lc.db}
	keepFrom := uint64(common.ScdoForkHeight)
	if head > uint64(RetainedHeaders) {
		keepFrom = head - uint64(RetainedHeaders)
	}
	for h := uint64(common.ScdoForkHeight); h <= head; h++ {
		hash, err := lc.bcStore.GetBlockHash(h)
		if err != nil {
			return errors.NewStackedErrorf(err, "cannot rebuild header accumulator, height %d is not stored", h)
		}
		if err = fresh.append(hash); err != nil {
			return err
		}
		fresh.tip = hash
		snap := h == common.ScdoForkHeight || h == head || h > keepFrom
		if snap || (h-common.ScdoForkHeight)%100000 == 0 {
			if err = fresh.save(h, snap); err != nil {
				return err
			}
		}
		if h > uint64(common.ScdoForkHeight) && (h-uint64(common.ScdoForkHeight))%100000 == 0 {
			lc.log.Info("header accumulator rebuilt through height %d", h)
		}
	}
	if err := fresh.save(head, true); err != nil {
		return err
	}
	lc.mmr = fresh
	return nil
}

// RewindVerified moves the accumulator back to a canonical height that is
// still stored. The header rows are removed by the downloader first. A reorg
// below the retained window has no snapshot and returns an error.
func (lc *LightChain) RewindVerified(height uint64) error {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()
	if lc.mmr == nil || lc.db == nil || height < common.ScdoForkHeight {
		return nil
	}
	want := height - common.ScdoForkHeight + 1
	if lc.mmr.count == want {
		return nil
	}
	if err := lc.mmr.restore(height); err != nil {
		return errors.NewStackedError(err, "reorg is deeper than the retained header window")
	}
	if lc.mmr.count != want {
		return fmt.Errorf("accumulator snapshot at height %d has %d leaves, want %d", height, lc.mmr.count, want)
	}
	return nil
}

// MMRLeaves is the number of header hashes committed after a successful ZPoW check.
func (lc *LightChain) MMRLeaves() uint64 {
	if lc == nil || lc.mmr == nil {
		return 0
	}
	return lc.mmr.count
}

// verifyCanonicalHeader checks header against the accumulator this chain built.
func (lc *LightChain) verifyCanonicalHeader(header *types.BlockHeader, siblings []common.Hash) error {
	if header == nil {
		return fmt.Errorf("header is missing")
	}
	if header.Height < common.ScdoForkHeight {
		return fmt.Errorf("header is before fork genesis")
	}
	if lc == nil || lc.mmr == nil || lc.mmr.count == 0 {
		return fmt.Errorf("header accumulator is empty")
	}
	index := header.Height - common.ScdoForkHeight
	if !lc.mmr.verify(index, header.Hash(), siblings) {
		return fmt.Errorf("header is not in the accumulator this node verified")
	}
	return nil
}
