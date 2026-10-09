package core

import (
	"testing"

	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
)

func TestAcceptAbsentSourceTxOnlyWhenSourceIsFarAhead(t *testing.T) {
	miss := errors.New("failed to get tx 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> leveldb: not found")
	far := errors.NewStackedError(errors.NewStackedErrorf(types.ErrSourceTxAbsent, "canonical height %d", 9273661), miss.Error())
	far = errors.NewStackedError(far, "failed to validate debt via verifier")
	if !acceptAbsentSourceTx(far, 5162247) {
		t.Fatal("shard1 block 5162247 should apply when shard2 at 9273661 has no such tx")
	}
	if acceptAbsentSourceTx(far, 9200000) {
		t.Fatal("a block near the source head must still wait for the tx")
	}
	behind := errors.NewStackedError(errors.NewStackedErrorf(types.ErrSourceTxAbsent, "canonical height %d", 4930667), miss.Error())
	if acceptAbsentSourceTx(behind, 5162247) {
		t.Fatal("a source node at 4930667 has not reached this block's era")
	}
	waiting := errors.NewStackedError(types.ErrHeaderNotReady, miss.Error())
	if acceptAbsentSourceTx(waiting, 5162247) {
		t.Fatal("not-ready without a caught-up source head must keep the block queued")
	}
}
