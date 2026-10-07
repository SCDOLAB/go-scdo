/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package p2p

import (
	"fmt"

	"github.com/scdoproject/go-scdo/p2p/discovery"
)

// classicBootHosts are the public SCDO Classic seed nodes.
// Ports follow the shard: 1=8057, 2=8058, 3=8059, 4=8056.
var classicBootHosts = []string{
	"74.208.207.184",
	"82.223.19.88",
	"74.208.136.152",
	"217.160.65.210",
}

var classicBootPorts = []int{8057, 8058, 8059, 8056}

// ClassicBootnodes returns ip:port for every public seed on every shard.
func ClassicBootnodes() []string {
	out := make([]string, 0, len(classicBootHosts)*len(classicBootPorts))
	for _, port := range classicBootPorts {
		for _, host := range classicBootHosts {
			out = append(out, fmt.Sprintf("%s:%d", host, port))
		}
	}
	return out
}

// MergeBootnodes appends any hard-coded bootnode that is not already in cfg.
// User staticNodes are kept. Discovery treats these as trusted UDP seeds.
func MergeBootnodes(cfg *Config) {
	if cfg == nil {
		return
	}
	have := make(map[string]struct{}, len(cfg.StaticNodes))
	for _, node := range cfg.StaticNodes {
		if node == nil {
			continue
		}
		have[node.GetUDPAddr().String()] = struct{}{}
	}
	for _, addr := range ClassicBootnodes() {
		node, err := discovery.NewNodeFromIP(addr)
		if err != nil {
			continue
		}
		key := node.GetUDPAddr().String()
		if _, ok := have[key]; ok {
			continue
		}
		cfg.StaticNodes = append(cfg.StaticNodes, node)
		have[key] = struct{}{}
	}
}
