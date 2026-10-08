/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/p2p/discovery"
)

// TestStopDuringHeaderSyncReturnsBeforeTheDBClose is the SIGTERM order from
// the a420ba8 field run: the header sync must leave before Stop returns, so
// the caller can close the LevelDB without a WriteHeader on a closed database.
func TestStopDuringHeaderSyncReturnsBeforeTheDBClose(t *testing.T) {
	lp, err := NewLightProtocol("test", nil, nil, nil, false, nil, log.GetLogger("light-stop-sync"), 2)
	if err != nil {
		t.Fatal(err)
	}
	d := lp.downloader
	d.lock.Lock()
	d.cancelCh = make(chan struct{})
	d.msgCh = make(chan *p2p.Message)
	d.syncStatus = statusDownloading
	d.wg.Add(1)
	d.lock.Unlock()

	releaseWrite := make(chan struct{})
	inWrite := make(chan struct{})
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	go func() {
		defer d.wg.Done()
		record("write-start")
		close(inWrite)
		<-releaseWrite
		if d.stopping() {
			record("write-abort")
			return
		}
		record("write-finished")
	}()
	<-inWrite
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(releaseWrite)
	}()

	begin := time.Now()
	lp.Stop()
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("Stop took %s while a header write was in progress", elapsed)
	}
	record("stop-returned")
	record("db-close")

	mu.Lock()
	defer mu.Unlock()
	pos := map[string]int{}
	for i, step := range order {
		pos[step] = i
	}
	for _, step := range []string{"write-abort", "stop-returned", "db-close"} {
		if _, ok := pos[step]; !ok {
			t.Fatalf("missing %s in %v", step, order)
		}
	}
	if pos["write-abort"] > pos["stop-returned"] || pos["stop-returned"] > pos["db-close"] {
		t.Fatalf("shutdown order %v", order)
	}

	// p2p is still dialing after Stop. A new peer must not touch the database.
	node := discovery.NewNode(common.BytesToAddress([]byte{9}), net.IPv4(127, 0, 0, 1), 0, 1)
	if lp.handleAddPeer(&p2p.Peer{Node: node}, nil) {
		t.Fatal("handleAddPeer accepted a peer after Stop")
	}
}

func TestDeliverMsgDoesNotBlockAfterCancel(t *testing.T) {
	d := newDownloader(nil)
	d.cancelCh = make(chan struct{})
	d.msgCh = make(chan *p2p.Message)
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.deliverMsg(nil, &p2p.Message{Code: downloadHeadersResponseCode})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deliverMsg blocked after the downloader was cancelled")
	}
}
