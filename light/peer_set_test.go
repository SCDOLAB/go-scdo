/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"net"
	"testing"

	"github.com/scdoproject/go-scdo/crypto"
	log2 "github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/p2p/discovery"
	"github.com/stretchr/testify/assert"
)

func getTestPeer(shard uint) *peer {
	log := log2.GetLogger("test")
	addr := crypto.MustGenerateRandomAddress()
	node := discovery.NewNodeWithAddr(*addr, &net.UDPAddr{}, shard)
	p2pPeer := p2p.NewPeer(nil, nil, node)
	peer := newPeer(1, p2pPeer, nil, log, nil)

	return peer
}

func TestSourceHeadAgreesWithVerifiedHeaders(t *testing.T) {
	if !sourceHeadAgrees(9273661, 9273000, 100000) {
		t.Fatal("a peer next to the verified shard2 header tip should count")
	}
	if sourceHeadAgrees(9273661, 4930667, 100000) {
		t.Fatal("local shard2 at 4930667 is not the verified header tip")
	}
	if sourceHeadAgrees(0, 9273661, 100000) || sourceHeadAgrees(9273661, 0, 100000) {
		t.Fatal("a missing height is not agreement")
	}
}

func TestCaughtUpPeersIgnoresAShardStillSyncing(t *testing.T) {
	set := newPeerSet()
	behind := getTestPeer(2)
	behind.headBlockNum = 4930667
	tip := getTestPeer(2)
	tip.headBlockNum = 9273661
	set.Add(behind)
	set.Add(tip)

	peers, best := set.caughtUpPeers(3, 100000)
	if best != 9273661 {
		t.Fatalf("best=%d", best)
	}
	if len(peers) != 1 || peers[0] != tip {
		t.Fatalf("caught-up peers = %v", peers)
	}
}

func Test_PeerSet_Add(t *testing.T) {
	set := newPeerSet()

	peer1 := getTestPeer(0)
	set.Add(peer1)
	assert.Equal(t, len(set.peerMap), 1)

	set.Add(peer1)
	assert.Equal(t, len(set.peerMap), 1)

	peer2 := getTestPeer(1)
	set.Add(peer2)
	assert.Equal(t, len(set.peerMap), 2)
}

func Test_PeerSet_Find(t *testing.T) {
	set := newPeerSet()
	peer1 := getTestPeer(0)
	set.Add(peer1)
	peer2 := getTestPeer(0)
	set.Add(peer2)

	assert.Equal(t, set.Find(peer1.Node.ID), peer1)
	assert.Equal(t, set.Find(peer2.Node.ID), peer2)
}

func Test_PeerSet_Remove(t *testing.T) {
	set := newPeerSet()
	peer1 := getTestPeer(0)
	set.Add(peer1)
	peer2 := getTestPeer(1)
	set.Add(peer2)

	assert.Equal(t, len(set.peerMap), 2)
	set.Remove(peer1.Node.ID)
	assert.Equal(t, len(set.peerMap), 1)
	set.Remove(peer1.Node.ID)
	assert.Equal(t, len(set.peerMap), 1)
	set.Remove(peer2.Node.ID)
	assert.Equal(t, len(set.peerMap), 0)
}
