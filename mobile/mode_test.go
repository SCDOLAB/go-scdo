/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package mobile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/light"
)

func TestParseModeAndShards(t *testing.T) {
	mode, err := ParseMode("")
	if err != nil || mode != ModeLite {
		t.Fatalf("mode %s %v", mode, err)
	}
	mode, err = ParseMode("PRO")
	if err != nil || mode != ModePro {
		t.Fatalf("mode %s %v", mode, err)
	}
	if _, err = ParseMode("archive"); err == nil {
		t.Fatal("expected mode error")
	}
	shards, err := ParseShards("1, 2,4")
	if err != nil || len(shards) != 3 || shards[0] != 1 || shards[2] != 4 {
		t.Fatalf("%v %v", shards, err)
	}
	shards, err = ParseShards("")
	if err != nil || len(shards) != 4 {
		t.Fatalf("%v %v", shards, err)
	}
	if _, err = ParseShards("5"); err == nil {
		t.Fatal("expected shard error")
	}
	metered, battery := DefaultSyncPolicy()
	if metered || !battery {
		t.Fatalf("metered=%v battery=%v", metered, battery)
	}
}

func TestDefaultPolicyAllowsMetered(t *testing.T) {
	light.Resume()
	metered, battery := DefaultSyncPolicy()
	light.SetSyncPolicy(metered, battery)
	light.SetDeviceState(true, false)
	t.Cleanup(func() {
		light.Resume()
		light.SetSyncPolicy(false, false)
		light.SetDeviceState(false, false)
	})
	paused, reason := light.SyncPause()
	if paused {
		t.Fatalf("5G should keep syncing, reason %s", reason)
	}
	if light.NetworkLabel() != "metered-allowed" {
		t.Fatal(light.NetworkLabel())
	}
	light.SetDeviceState(false, true)
	paused, reason = light.SyncPause()
	if !paused || reason != "low-battery" {
		t.Fatalf("paused=%v reason=%s", paused, reason)
	}
	if !strings.Contains(Status(), "node is not started") {
		t.Fatal(Status())
	}
}

func TestProDirsKeepLiteHeaders(t *testing.T) {
	dir := t.TempDir()
	lite := LiteShardDir(dir, 1)
	if err := os.MkdirAll(lite, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(lite, "verified-header")
	if err := os.WriteFile(marker, []byte("header"), 0600); err != nil {
		t.Fatal(err)
	}
	shards, err := ParseShards("1,2")
	if err != nil {
		t.Fatal(err)
	}
	if err = PrepareProDirs(dir, shards); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "header" {
		t.Fatalf("lite header missing: %v %q", err, body)
	}
	if LiteShardDir(dir, 1) == ProShardDir(dir, 1) {
		t.Fatal("lite and pro directories must differ")
	}
	if DirBytes(lite) == 0 {
		t.Fatal("expected lite bytes")
	}
	if proListenAddr(1) == proListenAddr(2) {
		t.Fatal("pro shards need different ports")
	}
	if proRPCAddr(1, 1) != RPCAddress || proRPCAddr(2, 1) == RPCAddress {
		t.Fatalf("rpc %s %s", proRPCAddr(1, 1), proRPCAddr(2, 1))
	}
}

func TestETAFromHeightSamples(t *testing.T) {
	s := &session{samples: map[uint]heightSample{}}
	if got := s.eta(1, 10, 100, true, "5m"); got != "paused" {
		t.Fatal(got)
	}
	if got := s.eta(1, 10, 110, false, ""); got != "unknown" {
		t.Fatal(got)
	}
	s.samples[1] = heightSample{height: 10, at: time.Now().Add(-10 * time.Second)}
	got := s.eta(1, 20, 120, false, "")
	if got != "1m40s" {
		t.Fatal(got)
	}
	if common.ScdoForkHeight != 2979594 {
		t.Fatal(common.ScdoForkHeight)
	}
}

func TestSetBootnodesReplacesPublicSeeds(t *testing.T) {
	const url = "snode://0101f3c956d0a320b153a097c3d04efa488d43d1@192.168.50.50:10057[1]"
	if err := SetBootnodes(url); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetBootnodes("") })

	conf, err := baseConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(conf.P2PConfig.StaticNodes) != 1 {
		t.Fatalf("bootnodes %d, want 1", len(conf.P2PConfig.StaticNodes))
	}
	node := conf.P2PConfig.StaticNodes[0]
	if node.UDPPort != 10057 || node.Shard != 1 || node.IP.String() != "192.168.50.50" {
		t.Fatalf("bootnode %+v", node)
	}
	if !strings.Contains(node.String(), "192.168.50.50:10057") {
		t.Fatal(node.String())
	}
}
