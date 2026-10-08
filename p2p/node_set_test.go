/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package p2p

import (
	"testing"

	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/p2p/discovery"
)

func getNode() *discovery.Node {
	return discovery.NewNode(*crypto.MustGenerateRandomAddress(), nil, 0, 1)
}

func Test_NodeSet(t *testing.T) {
	set := NewNodeSet()

	p1 := getNode()
	set.tryAdd(p1)

	srv := &Server{peerSet: NewPeerSet()}
	selected := set.randSelect(srv)
	if len(selected) == 0 {
		t.Fatalf("should select one node.")
	}

	set.delete(selected[0])
	if len(set.randSelect(srv)) != 0 {
		t.Fatalf("should select no node.")
	}
}
