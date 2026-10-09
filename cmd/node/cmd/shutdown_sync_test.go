/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus/factory"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/monitor"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/scdo"
	"github.com/scdoproject/go-scdo/scdo/lightclients"
)

// stallPeer accepts header requests and never answers them.
type stallPeer struct{}

func (stallPeer) Head() (common.Hash, *big.Int) {
	return common.BytesToHash([]byte{1}), big.NewInt(1)
}
func (stallPeer) RequestHeadersByHashOrNumber(uint32, common.Hash, uint64, int, bool) error {
	return nil
}
func (stallPeer) RequestBlocksByHashOrNumber(uint32, common.Hash, uint64, int) error {
	return nil
}
func (stallPeer) GetPeerRequestInfo() (uint32, common.Hash, uint64, int) {
	return 0, common.EmptyHash, 0, 0
}
func (stallPeer) DisconnectPeer(string) {}

// TestFullAndLightStopDuringSync is the service order a full node uses on
// SIGTERM: other-shard header clients, then the full chain, while a download
// is waiting on a peer. Stop has to return inside the 20s budget and leave a
// clean checkpoint at or below the canonical head.
func TestFullAndLightStopDuringSync(t *testing.T) {
	dir := t.TempDir()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	conf := &node.Config{
		BasicConfig: node.BasicConfig{
			Name:           "scdo-test",
			Version:        "test",
			DataDir:        dir,
			RPCAddr:        "127.0.0.1:0",
			MinerAlgorithm: common.ZpowAlgorithm,
		},
		P2PConfig: p2p.Config{
			ListenAddr: "127.0.0.1:0",
			NetworkID:  "net1",
			PrivateKey: key,
		},
		HTTPServer: node.HTTPServer{
			HTTPAddr:      "127.0.0.1:0",
			HTTPCors:      []string{"*"},
			HTTPWhiteHost: []string{"*"},
		},
		WSServerConfig: node.WSServerConfig{Address: "127.0.0.1:0"},
		IpcConfig:      node.IpcConfig{PipeName: filepath.Join(dir, "scdo.ipc")},
		ScdoConfig: node.ScdoConfig{
			TxConf: *core.DefaultTxPoolConfig(),
			GenesisConfig: core.GenesisInfo{
				Difficult:       1900000,
				ShardNumber:     1,
				CreateTimestamp: big.NewInt(1596942480),
			},
		},
	}

	scdoNode, err := node.New(conf)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := factory.GetConsensusEngine(common.ZpowAlgorithm)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), "ServiceContext", scdo.ServiceContext{DataDir: dir})
	manager, err := lightclients.NewLightClientManager(scdoNode.GetShardNumber(), ctx, conf, engine)
	if err != nil {
		t.Fatal(err)
	}
	scdolog := log.GetLogger("scdo")
	scdoService, err := scdo.NewScdoService(ctx, conf, scdolog, engine, manager, -1, false)
	if err != nil {
		t.Fatal(err)
	}
	scdoService.Miner().SetThreads(1)
	scdoService.Miner().SetStopper(1)
	monitorService, err := monitor.NewMonitorService(scdoService, scdoNode, conf, scdolog, "test")
	if err != nil {
		t.Fatal(err)
	}
	lightServer, err := light.NewServiceServer(scdoService, conf, log.GetLogger("scdo-light"), scdoNode.GetShardNumber())
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range append(manager.GetServices(), scdoService, monitorService, lightServer) {
		if err = scdoNode.Register(service); err != nil {
			t.Fatal(err)
		}
	}

	dl := scdoService.Downloader()
	dl.RegisterPeer("stall", stallPeer{})
	syncDone := make(chan error, 1)
	go func() {
		syncDone <- dl.Synchronise("stall", common.BytesToHash([]byte{1}))
	}()
	time.Sleep(100 * time.Millisecond)

	if err = scdoNode.Start(); err != nil {
		t.Fatalf("start: %s", err)
	}
	scdoService.Miner().Stop()

	begin := time.Now()
	if err = stopWithin(5*time.Second, scdoNode.Stop); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("stop took %s", elapsed)
	}
	select {
	case err = <-syncDone:
		if err == nil || err.Error() == "Peer not found" {
			t.Fatalf("sync did not block on the stall peer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download session still running after Stop")
	}

	head := scdoService.BlockChain().CurrentBlock().Header.Height
	data, err := os.ReadFile(filepath.Join(dir, "indexCheckpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cp struct {
		VerifiedHeight uint64 `json:"verifiedHeight"`
		Clean          bool   `json:"clean"`
	}
	if err = json.Unmarshal(data, &cp); err != nil {
		t.Fatal(err)
	}
	if !cp.Clean {
		t.Fatalf("checkpoint not clean: %s", data)
	}
	if cp.VerifiedHeight > head {
		t.Fatalf("checkpoint %d is above canonical head %d", cp.VerifiedHeight, head)
	}
}
