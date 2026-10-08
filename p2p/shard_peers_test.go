/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package p2p

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p/discovery"
)

func TestShardsWithoutPeers(t *testing.T) {
	set := NewNodeSet()
	if len(set.shardsWithoutPeers()) != common.ShardCount {
		t.Fatalf("fresh set missing %d shards, want %d", len(set.shardsWithoutPeers()), common.ShardCount)
	}

	n1 := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.0.0.1"), 8057, 1)
	set.tryAdd(n1)
	set.setNodeStatus(n1, true)
	for _, shard := range set.shardsWithoutPeers() {
		if shard == 1 {
			t.Fatal("connected shard 1 was reported missing")
		}
	}

	n2 := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.0.0.2"), 8057, 2)
	set.tryAdd(n2)
	got := set.unconnected(2, 3)
	if len(got) != 1 || !got[0].ID.Equal(n2.ID) {
		t.Fatalf("unconnected shard 2 nodes = %d", len(got))
	}
}

func TestGroupDialListCapsEachShard(t *testing.T) {
	nodes := make(map[common.Hash]*discovery.Node)
	for i := 0; i < 20; i++ {
		n := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.1.0.1"), 8000+i, 1)
		nodes[common.StringToHash(fmt.Sprintf("s1-%d", i))] = n
	}
	for i := 0; i < 3; i++ {
		n := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.2.0.1"), 9000+i, 2)
		nodes[common.StringToHash(fmt.Sprintf("s2-%d", i))] = n
	}
	chosen := groupDialList(nodes, knownPeersPerShard, 0)
	counts := map[uint]int{}
	for _, n := range chosen {
		counts[n.Shard]++
	}
	if counts[1] != knownPeersPerShard || counts[2] != 3 {
		t.Fatalf("dial counts = %v", counts)
	}
}

func TestGroupDialListDialsLocalShardFirst(t *testing.T) {
	nodes := make(map[common.Hash]*discovery.Node)
	for i := 0; i < 20; i++ {
		n := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.1.0.1"), 8000+i, 1)
		nodes[common.StringToHash(fmt.Sprintf("s1-%d", i))] = n
	}
	for i := 0; i < 20; i++ {
		n := discovery.NewNode(*crypto.MustGenerateRandomAddress(), net.ParseIP("10.3.0.1"), 9000+i, 3)
		nodes[common.StringToHash(fmt.Sprintf("s3-%d", i))] = n
	}
	chosen := groupDialList(nodes, knownPeersPerShard, 3)
	if len(chosen) < knownPeersPerShard*2 {
		t.Fatalf("chosen %d", len(chosen))
	}
	local := 0
	seenOther := false
	for _, n := range chosen {
		if n.Shard == 3 {
			if seenOther {
				t.Fatal("local shard peer dialed after another shard")
			}
			local++
			continue
		}
		seenOther = true
	}
	if local != knownPeersPerShard*2 {
		t.Fatalf("local shard dials = %d, want %d", local, knownPeersPerShard*2)
	}
	order := preferShard([]uint{1, 2, 3, 4}, 3)
	if len(order) != 4 || order[0] != 3 {
		t.Fatalf("prefer order %v", order)
	}
}

func TestSeekShardPeersWithoutUDP(t *testing.T) {
	srv := &Server{
		nodeSet:  NewNodeSet(),
		log:      log.GetLogger("p2p"),
		lastSeek: make([]time.Time, common.ShardCount+1),
	}
	srv.SeekShardPeers(3)
	srv.SeekShardPeers(3)
	if time.Since(srv.lastSeek[3]) > time.Second {
		t.Fatal("seek was not recorded")
	}
}
