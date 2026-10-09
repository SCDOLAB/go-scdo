package core

import (
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
)

func TestHistoricalAllowlistIsTheReviewedDebtOnly(t *testing.T) {
	absent, err := common.HexToHash("0x46c6d99968db700d744b6542b05e32453e48c11d8a52a1698d4e631696e3b259")
	if err != nil {
		t.Fatal(err)
	}
	if !HistoricalAbsentDebt(1, 5162247, absent) {
		t.Fatal("shard1 block 5162247 debt 0x46c6 is the reviewed missing source tx")
	}
	if HistoricalAbsentDebt(2, 5162247, absent) || HistoricalAbsentDebt(1, 5162248, absent) {
		t.Fatal("the same debt hash on another shard or height is not allowlisted")
	}
	// The other two debts in that block have source transactions on shard3.
	present, err := common.HexToHash("0x3f71d4202797edc9e378adf070861e12fdaa27684427c75da4487effcb088818")
	if err != nil {
		t.Fatal(err)
	}
	if HistoricalAbsentDebt(1, 5162247, present) {
		t.Fatal("a debt whose source tx is on the canonical chain stays strict")
	}
	later, err := common.HexToHash("0x17578564a079bc1b0a83d9128a8ac43f343c2f2e04d7a0d10598a66930c7ca4a")
	if err != nil {
		t.Fatal(err)
	}
	if !HistoricalAbsentDebt(1, 6124420, later) {
		t.Fatal("the 2023-11-11 shard3-source debt is on the allowlist")
	}
	if len(historicalAbsentDebt) != 9 {
		t.Fatalf("allowlist len = %d, want the 9 debts missing from the public canonical chains", len(historicalAbsentDebt))
	}
}

func TestStrictMissStillFailsValidation(t *testing.T) {
	miss := errors.New("failed to get tx 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> leveldb: not found")
	if !types.ShardDataNotReady(miss) {
		t.Fatal("a source-tx miss must stay retryable for debts that are not on the allowlist")
	}
	waiting := errors.NewStackedError(types.ErrHeaderNotReady, "wait for msg reqid=1 timeout")
	if !types.ShardDataNotReady(waiting) {
		t.Fatal("an ODR timeout must stay retryable")
	}
	proof := errors.New("failed to handle ODR request on server side ===> failed to prove merkle trie")
	if types.ShardDataNotReady(proof) {
		t.Fatal("a proof failure must still reject the block")
	}
}
