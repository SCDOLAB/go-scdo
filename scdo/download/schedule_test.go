/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runSchedule models one sync window.
// serialHeader is the old loop: a peer finishes a header round trip before it
// asks for a body, and only one body batch is in flight.
// The pipelined loop overlaps headers and keeps inflight body batches.
func runSchedule(peers, batch, inflight int, serialHeader bool, rtt, xferPerBlock time.Duration, window time.Duration) float64 {
	var blocks int64
	var wg sync.WaitGroup
	deadline := time.Now().Add(window)
	for p := 0; p < peers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if serialHeader {
				for time.Now().Before(deadline) {
					time.Sleep(rtt)
					time.Sleep(rtt + time.Duration(batch)*xferPerBlock)
					atomic.AddInt64(&blocks, int64(batch))
				}
				return
			}
			sem := make(chan struct{}, inflight)
			var inner sync.WaitGroup
			for time.Now().Before(deadline) {
				select {
				case sem <- struct{}{}:
				default:
					time.Sleep(rtt)
					continue
				}
				inner.Add(1)
				go func() {
					defer inner.Done()
					defer func() { <-sem }()
					time.Sleep(rtt + time.Duration(batch)*xferPerBlock)
					atomic.AddInt64(&blocks, int64(batch))
				}()
			}
			inner.Wait()
		}()
	}
	wg.Wait()
	return float64(blocks) / window.Seconds()
}

func TestSchedulePipelineFasterThanSerial(t *testing.T) {
	const (
		peers = 2
		rtt   = 8 * time.Millisecond
		xfer  = time.Millisecond
		win   = 250 * time.Millisecond
	)
	old := runSchedule(peers, 32, 1, true, rtt, xfer, win)
	neu := runSchedule(peers, MaxBlockFetch, maxBodyInflight, false, rtt, xfer, win)
	t.Logf("modeled sync: serial %.1f blk/s, pipelined %.1f blk/s, speedup %.2fx", old, neu, neu/old)
	if neu < old*1.4 {
		t.Fatalf("pipelined schedule %.1f blk/s, serial %.1f blk/s", neu, old)
	}
}
