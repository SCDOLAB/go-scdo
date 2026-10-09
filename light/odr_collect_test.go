package light

import (
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/scdoproject/go-scdo/p2p"
)

func testODR(t *testing.T) *odrBackend {
	t.Helper()
	db, dispose := leveldb.NewTestDatabase()
	t.Cleanup(dispose)
	return newOdrBackend(store.NewBlockchainDatabase(db), 1)
}

func txResponse(reqID uint32, errText string) *odrTxByHashResponse {
	resp := &odrTxByHashResponse{}
	resp.ReqID = reqID
	resp.Error = errText
	return resp
}

func TestCollectODRSkipsPeerThatLacksTheTx(t *testing.T) {
	o := testODR(t)
	ch := make(chan odrResponse, 2)
	ch <- txResponse(1, "failed to get tx by hash 0xd9cc ===> leveldb: not found")
	ch <- txResponse(1, "")

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	start := time.Now()
	resp, err := o.collectResponses(ch, 2, 1, timer)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.getError() != nil {
		t.Fatalf("expected the second peer's tx, got %v", resp)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a later peer was not accepted until the timeout")
	}
}

func TestCollectODRAllPeersMissingIsRetryable(t *testing.T) {
	o := testODR(t)
	ch := make(chan odrResponse, 2)
	ch <- txResponse(1, "failed to get tx by hash 0xd9cc ===> leveldb: not found")
	ch <- txResponse(1, "failed to get tx by hash 0xd9cc ===> leveldb: not found")

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	_, err := o.collectResponses(ch, 2, 1, timer)
	if !errors.IsOrContains(err, types.ErrHeaderNotReady) {
		t.Fatalf("missing tx should wait, got %v", err)
	}
	if !types.ShardDataNotReady(err) {
		t.Fatalf("downloader would reject the block: %v", err)
	}
}

func TestCollectODRTimeoutIsRetryable(t *testing.T) {
	o := testODR(t)
	ch := make(chan odrResponse, 1)
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	_, err := o.collectResponses(ch, 1, 70714341, timer)
	if !errors.IsOrContains(err, types.ErrHeaderNotReady) {
		t.Fatalf("timeout should wait, got %v", err)
	}
	if !types.ShardDataNotReady(errors.New("failed to get tx 0x72ba ===> " + err.Error())) {
		t.Fatal(err)
	}
}

func TestCollectODRProofErrorStillFails(t *testing.T) {
	o := testODR(t)
	ch := make(chan odrResponse, 2)
	ch <- txResponse(1, "failed to prove merkle trie")

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	start := time.Now()
	_, err := o.collectResponses(ch, 2, 1, timer)
	if err == nil || errors.IsOrContains(err, types.ErrHeaderNotReady) {
		t.Fatalf("a proof failure must still reject the response, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("proof failure waited for the other peers")
	}
}

func TestHandleResponseDeliversEveryPeer(t *testing.T) {
	o := testODR(t)
	ch := make(chan odrResponse, 2)
	o.lock.Lock()
	o.requestMap[7] = ch
	o.lock.Unlock()

	send := func(errText string) {
		payload := common.SerializePanic(txResponse(7, errText))
		o.handleResponse(&p2p.Message{Code: txByHashResponseCode, Payload: payload})
	}
	send("failed to get tx by hash 0x1 ===> leveldb: not found")
	send("")

	first := <-ch
	second := <-ch
	if first.getError() == nil {
		t.Fatal("first peer miss was dropped")
	}
	if second.getError() != nil {
		t.Fatalf("second peer was dropped: %v", second.getError())
	}
}
