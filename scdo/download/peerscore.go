/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

// peerStat is one peer's block-delivery record for this sync session.
type peerStat struct {
	blocks  uint64
	seconds float64
	strikes int
}

const (
	minPeersToDrop   = 3
	slowStrikeDrop   = 3
	minBlocksToScore = 64
	slowFraction     = 0.25
)

// dropPeer reports whether peer id is slow enough to disconnect.
// The master peer is kept: it is the header source for this session.
// A peer is dropped only when at least minPeersToDrop peers are connected,
// so the node does not isolate itself.
func dropPeer(id string, stats map[string]peerStat, peerCount int, isMaster bool) bool {
	if isMaster || peerCount < minPeersToDrop {
		return false
	}
	st, ok := stats[id]
	if !ok {
		return false
	}
	if st.strikes >= slowStrikeDrop {
		return true
	}
	if st.blocks < minBlocksToScore || st.seconds <= 0 {
		return false
	}
	rate := float64(st.blocks) / st.seconds
	best := 0.0
	for _, other := range stats {
		if other.seconds <= 0 || other.blocks == 0 {
			continue
		}
		r := float64(other.blocks) / other.seconds
		if r > best {
			best = r
		}
	}
	return best > 0 && rate < best*slowFraction
}
