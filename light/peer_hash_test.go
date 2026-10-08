/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"fmt"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/log"
)

// TestHashWindowStaysBounded feeds a full-chain worth of hash batches and
// checks the peer keeps only the sliding window.
func TestHashWindowStaysBounded(t *testing.T) {
	p := &peer{
		log:       log.GetLogger("light-peer-test"),
		hashIndex: make(map[common.Hash]uint64),
	}
	const batches = 40 // 40 * 1024 = 40960 hashes, far past the window
	var prev common.Hash
	begin := uint64(2_979_594)
	var first common.Hash
	for n := 0; n < batches; n++ {
		hashes := make([]common.Hash, MaxBlockHashRequest)
		for i := range hashes {
			hashes[i] = common.StringToHash(fmt.Sprintf("sync-hash-%d-%d", n, i))
		}
		if n == 0 {
			first = hashes[0]
			p.setHashWindow(begin, hashes)
		} else {
			hashes[0] = prev
			idx := p.findIdxByHashLocked(hashes[0])
			if idx < 0 {
				t.Fatalf("batch %d overlap hash was trimmed", n)
			}
			p.replaceFrom(idx, hashes)
		}
		prev = hashes[len(hashes)-1]
		if len(p.blockHashArr) > maxPeerHashWindow {
			t.Fatalf("batch %d retained %d hashes, cap is %d", n, len(p.blockHashArr), maxPeerHashWindow)
		}
		if cap(p.blockHashArr) > maxPeerHashWindow {
			t.Fatalf("batch %d slice cap %d, want <= %d", n, cap(p.blockHashArr), maxPeerHashWindow)
		}
	}

	if len(p.blockHashArr) != maxPeerHashWindow {
		t.Fatalf("window len %d, want %d", len(p.blockHashArr), maxPeerHashWindow)
	}
	if len(p.hashIndex) != maxPeerHashWindow {
		t.Fatalf("index len %d, want %d", len(p.hashIndex), maxPeerHashWindow)
	}
	if p.findIdxByHashLocked(prev) < 0 {
		t.Fatal("newest hash is not indexed")
	}
	if p.findIdxByHashLocked(first) >= 0 {
		t.Fatal("hash from the start of the chain is still retained")
	}
	if p.anchorHash != first || p.anchorNum != begin {
		t.Fatalf("anchor moved to %d %s", p.anchorNum, p.anchorHash.Hex())
	}
	// Lookup is a map read. Scanning the window must not be required.
	if _, ok := p.hashIndex[prev]; !ok {
		t.Fatal("newest hash missing from the index")
	}
}
