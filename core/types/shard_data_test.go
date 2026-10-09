package types

import (
	"testing"

	"github.com/scdoproject/go-scdo/common/errors"
)

func TestShardDataNotReadyKeepsTxMissAndRejectsBadProof(t *testing.T) {
	miss := errors.New("failed to apply block txs ===> failed to batch validate debt ===> failed to validate debt via verifier ===> failed to get tx 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> failed to handle ODR request on server side ===> failed to get tx by hash 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> leveldb: not found")
	if !ShardDataNotReady(miss) {
		t.Fatal("ODR not found must be retried")
	}
	timeout := errors.New("failed to get tx 0x72ba09d19faa24ee0bf804b5fa74151fc9814fb3235e0a0eef41a7be776d5fd0 ===> wait for msg reqid=70714341 timeout")
	if !ShardDataNotReady(timeout) {
		t.Fatal("ODR timeout must be retried")
	}
	if !ShardDataNotReady(errors.NewStackedError(ErrHeaderNotReady, "No peers found")) {
		t.Fatal("missing peers must be retried")
	}
	if ShardDataNotReady(errors.New("failed to get block header ===> leveldb: not found")) {
		t.Fatal("a missing local block must still fail the write")
	}
	if ShardDataNotReady(errors.New("failed to handle ODR request on server side ===> failed to prove merkle trie")) {
		t.Fatal("a bad proof must still reject the debt")
	}
	if ShardDataNotReady(nil) {
		t.Fatal("nil error is not a wait")
	}
}
