package core

import "github.com/scdoproject/go-scdo/common"

// debtAllowKey is one reviewed historical debt: the shard that packed it,
// that block's height, and the debt hash.
type debtAllowKey struct {
	shard  uint32
	height uint64
	hash   common.Hash
}

// historicalAbsentDebt lists debts whose source transaction is absent from
// the canonical source shard. Populated from historicalAbsentDebtRows.
var historicalAbsentDebt map[debtAllowKey]struct{}

func init() {
	historicalAbsentDebt = make(map[debtAllowKey]struct{}, len(historicalAbsentDebtRows))
	for _, row := range historicalAbsentDebtRows {
		hash, err := common.HexToHash(row.hash)
		if err != nil {
			panic("historical debt hash: " + row.hash)
		}
		historicalAbsentDebt[debtAllowKey{shard: row.shard, height: row.height, hash: hash}] = struct{}{}
	}
}

// HistoricalAbsentDebt reports whether this debt was reviewed and its source
// transaction is not on the canonical source chain. Only that pair may be
// applied without the source proof. The same hash at another height, or
// another debt in the same block, stays under strict validation.
func HistoricalAbsentDebt(shard uint, height uint64, hash common.Hash) bool {
	if historicalAbsentDebt == nil {
		return false
	}
	_, ok := historicalAbsentDebt[debtAllowKey{shard: uint32(shard), height: height, hash: hash}]
	return ok
}
