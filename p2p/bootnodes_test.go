/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package p2p

import "testing"

func TestMergeBootnodesKeepsUserNodesAndAddsSeeds(t *testing.T) {
	cfg := &Config{}
	MergeBootnodes(cfg)
	if len(cfg.StaticNodes) != len(ClassicBootnodes()) {
		t.Fatalf("got %d bootnodes, want %d", len(cfg.StaticNodes), len(ClassicBootnodes()))
	}
	first := len(cfg.StaticNodes)
	MergeBootnodes(cfg)
	if len(cfg.StaticNodes) != first {
		t.Fatalf("second merge grew the list from %d to %d", first, len(cfg.StaticNodes))
	}
	seen := map[string]int{}
	for _, node := range cfg.StaticNodes {
		seen[node.GetUDPAddr().String()]++
	}
	for _, addr := range ClassicBootnodes() {
		if seen[addr] != 1 {
			t.Fatalf("bootnode %s count = %d", addr, seen[addr])
		}
	}
}
