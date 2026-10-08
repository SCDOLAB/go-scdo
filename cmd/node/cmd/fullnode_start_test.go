/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package cmd

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"

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

// TestFullNodeStartWithLightServer is the startup path the .50 nodes use:
// default full node, --lightserver left on. rpc.Server rejects an unexported
// service type, and that used to abort here with "headerProofAPI is not exported".
func TestFullNodeStartWithLightServer(t *testing.T) {
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

	services := manager.GetServices()
	services = append(services, scdoService, monitorService, lightServer)
	for _, service := range services {
		if err := scdoNode.Register(service); err != nil {
			t.Fatal(err)
		}
	}

	if err := scdoNode.Start(); err != nil {
		t.Fatalf("full node with light server failed to start: %s", err)
	}
	scdoService.Miner().Stop()
	if err := scdoNode.Stop(); err != nil {
		t.Fatal(err)
	}
}
