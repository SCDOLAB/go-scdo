/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/database"
	leveldbErrors "github.com/syndtr/goleveldb/leveldb/errors"
)

// mmr is a Merkle mountain range of header hashes this node has already
// verified. Peaks are oldest-first. A leaf is committed only after ZPoW
// succeeds and the header becomes canonical, so an inclusion proof against
// these peaks is a proof against data the phone itself checked.
type mmr struct {
	count uint64
	peaks []common.Hash
	sizes []uint64
	tip   common.Hash
}

type storedMMR struct {
	Count uint64
	Peaks []common.Hash
	Tip   common.Hash
}

// chainMMR is an MMR persisted in the light chain database.
type chainMMR struct {
	mmr
	db database.Database
}

var mmrTipKey = []byte("light-mmr-tip")

func mmrSnapKey(height uint64) []byte {
	buf := make([]byte, len("light-mmr-snap-")+8)
	copy(buf, "light-mmr-snap-")
	binary.BigEndian.PutUint64(buf[len("light-mmr-snap-"):], height)
	return buf
}

func hashLeaf(leaf common.Hash) common.Hash {
	return crypto.HashBytes([]byte{0x00}, leaf.Bytes())
}

func hashNode(left, right common.Hash) common.Hash {
	return crypto.HashBytes([]byte{0x01}, left.Bytes(), right.Bytes())
}

// peakSizes lists mountain sizes oldest-first. They are the set bits of
// count from high to low, so the newest peak is the smallest mountain.
func peakSizes(count uint64) []uint64 {
	sizes := make([]uint64, 0, bits.OnesCount64(count))
	for bit := uint64(1) << 63; bit > 0; bit >>= 1 {
		if count&bit != 0 {
			sizes = append(sizes, bit)
		}
	}
	return sizes
}

func (m mmr) clone() mmr {
	out := m
	out.peaks = append([]common.Hash(nil), m.peaks...)
	out.sizes = append([]uint64(nil), m.sizes...)
	return out
}

func (m *mmr) append(leaf common.Hash) error {
	if m.count > 0 && len(m.sizes) != len(m.peaks) {
		m.sizes = peakSizes(m.count)
	}
	if m.count > 0 && uint(bits.OnesCount64(m.count)) != uint(len(m.peaks)) {
		return fmt.Errorf("header accumulator peaks do not match its leaf count")
	}
	node := hashLeaf(leaf)
	size := uint64(1)
	for len(m.peaks) > 0 && m.sizes[len(m.sizes)-1] == size {
		node = hashNode(m.peaks[len(m.peaks)-1], node)
		m.peaks = m.peaks[:len(m.peaks)-1]
		m.sizes = m.sizes[:len(m.sizes)-1]
		size <<= 1
	}
	m.peaks = append(m.peaks, node)
	m.sizes = append(m.sizes, size)
	m.count++
	if uint(bits.OnesCount64(m.count)) != uint(len(m.peaks)) {
		return fmt.Errorf("header accumulator merge did not match the leaf count")
	}
	return nil
}

// verify checks that leaf is the header hash at index, using siblings from
// the mountain that contains it. The mountain size must be 1<<len(siblings)
// and the peak must be aligned to that size.
func (m *mmr) verify(index uint64, leaf common.Hash, siblings []common.Hash) bool {
	if m.count == 0 || index >= m.count {
		return false
	}
	sizes := m.sizes
	if len(sizes) != len(m.peaks) {
		sizes = peakSizes(m.count)
	}
	var start uint64
	mountain := -1
	for i, sz := range sizes {
		if index < start+sz {
			mountain = i
			break
		}
		start += sz
	}
	if mountain < 0 || sizes[mountain] == 0 {
		return false
	}
	if bits.Len64(sizes[mountain])-1 != len(siblings) {
		return false
	}
	if uint64(1)<<uint(len(siblings)) != sizes[mountain] {
		return false
	}
	rel := index - start
	node := hashLeaf(leaf)
	for _, sib := range siblings {
		if rel&1 == 1 {
			node = hashNode(sib, node)
		} else {
			node = hashNode(node, sib)
		}
		rel >>= 1
	}
	if rel != 0 {
		return false
	}
	return node.Equal(m.peaks[mountain])
}

// ProveHashes builds an inclusion proof for leaves[index] against the MMR of
// the whole slice. The full node has the headers; the phone only has peaks.
func ProveHashes(leaves []common.Hash, index uint64) ([]common.Hash, error) {
	n := uint64(len(leaves))
	if index >= n {
		return nil, fmt.Errorf("leaf index %d is outside the accumulator of %d headers", index, n)
	}
	sizes := peakSizes(n)
	var start uint64
	var size uint64
	for _, sz := range sizes {
		if index < start+sz {
			size = sz
			break
		}
		start += sz
	}
	if size == 0 || start%size != 0 {
		return nil, fmt.Errorf("leaf index %d is not inside an aligned mountain", index)
	}
	return proveMountain(leaves, start, size, index), nil
}

func proveMountain(leaves []common.Hash, start, size, index uint64) []common.Hash {
	if size <= 1 {
		return []common.Hash{}
	}
	level := make([]common.Hash, size)
	for i := uint64(0); i < size; i++ {
		level[i] = hashLeaf(leaves[start+i])
	}
	rel := index - start
	siblings := make([]common.Hash, 0, bits.Len64(size-1))
	for len(level) > 1 {
		siblings = append(siblings, level[rel^1])
		next := make([]common.Hash, len(level)/2)
		for i := range next {
			next[i] = hashNode(level[2*i], level[2*i+1])
		}
		level = next
		rel >>= 1
	}
	return siblings
}

func decodeMMR(raw []byte) (mmr, error) {
	var st storedMMR
	if err := common.Deserialize(raw, &st); err != nil {
		return mmr{}, err
	}
	if st.Count > 0 && uint(bits.OnesCount64(st.Count)) != uint(len(st.Peaks)) {
		return mmr{}, fmt.Errorf("stored header accumulator has %d peaks for %d leaves", len(st.Peaks), st.Count)
	}
	return mmr{count: st.Count, peaks: st.Peaks, sizes: peakSizes(st.Count), tip: st.Tip}, nil
}

func loadChainMMR(db database.Database) (*chainMMR, error) {
	m := &chainMMR{db: db}
	if db == nil {
		return m, nil
	}
	raw, err := db.Get(mmrTipKey)
	if err != nil {
		if isNotFound(err) {
			return m, nil
		}
		return nil, err
	}
	state, err := decodeMMR(raw)
	if err != nil {
		return nil, err
	}
	m.mmr = state
	return m, nil
}

func (m *chainMMR) save(height uint64, snap bool) error {
	if m == nil || m.db == nil {
		return fmt.Errorf("header accumulator has no database")
	}
	raw, err := common.Serialize(storedMMR{Count: m.count, Peaks: m.peaks, Tip: m.tip})
	if err != nil {
		return err
	}
	batch := m.db.NewBatch()
	batch.Put(mmrTipKey, raw)
	if snap {
		batch.Put(mmrSnapKey(height), raw)
	}
	return batch.Commit()
}

// commit appends one verified header hash and writes the tip and the
// height snapshot. The in-memory peaks change only after the write succeeds.
func (m *chainMMR) commit(leaf common.Hash, height uint64) error {
	next := m.mmr.clone()
	if err := next.append(leaf); err != nil {
		return err
	}
	next.tip = leaf
	saved := m.mmr
	m.mmr = next
	if err := m.save(height, true); err != nil {
		m.mmr = saved
		return err
	}
	return nil
}

func (m *chainMMR) restore(height uint64) error {
	if m == nil || m.db == nil {
		return fmt.Errorf("header accumulator has no database")
	}
	raw, err := m.db.Get(mmrSnapKey(height))
	if err != nil {
		return err
	}
	state, err := decodeMMR(raw)
	if err != nil {
		return err
	}
	saved := m.mmr
	m.mmr = state
	if err = m.save(height, false); err != nil {
		m.mmr = saved
		return err
	}
	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return err == leveldbErrors.ErrNotFound
}
