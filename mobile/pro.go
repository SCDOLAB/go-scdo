/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package mobile

import (
	"context"
	"fmt"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus/factory"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/scdo"
	"github.com/scdoproject/go-scdo/scdo/lightclients"
)

func startLite(dataDir string, shards []uint) (*session, error) {
	conf, err := baseConfig(dataDir)
	if err != nil {
		return nil, err
	}
	conf.BasicConfig.Name = "SCDO Light"
	conf.HTTPServer.HTTPAddr = RPCAddress
	conf.P2PConfig.ListenAddr = P2PAddress
	conf.ScdoConfig.GenesisConfig.ShardNumber = shards[0]
	if err := loadP2PKey(&conf.P2PConfig, dataDir); err != nil {
		return nil, err
	}
	engine, err := factory.GetConsensusEngine(common.ZpowAlgorithm)
	if err != nil {
		return nil, err
	}
	ctx := serviceContext(dataDir)
	n, err := node.New(conf)
	if err != nil {
		return nil, err
	}
	clients, err := light.OpenShardSet(ctx, conf, engine, shards)
	if err != nil {
		return nil, err
	}
	for _, client := range clients {
		if client == nil {
			continue
		}
		if err = n.Register(client); err != nil {
			return nil, err
		}
	}
	service, api := light.Bind(clients)
	if err = n.Register(service); err != nil {
		return nil, err
	}
	if err = n.Start(); err != nil {
		n.Stop()
		return nil, err
	}
	return &session{
		mode:    ModeLite,
		shards:  append([]uint(nil), shards...),
		dataDir: dataDir,
		nodes:   []*node.Node{n},
		api:     api,
		samples: map[uint]heightSample{},
	}, nil
}

func startPro(dataDir string, shards []uint) (*session, error) {
	// Full-node directories are separate. Verified lite headers stay put.
	if err := PrepareProDirs(dataDir, shards); err != nil {
		return nil, err
	}
	engine, err := factory.GetConsensusEngine(common.ZpowAlgorithm)
	if err != nil {
		return nil, err
	}
	// Debt checks reuse the lite header databases under dataDir/db.
	// One manager owns each database. The full shards share it.
	liteCtx := serviceContext(dataDir)
	liteConf, err := baseConfig(dataDir)
	if err != nil {
		return nil, err
	}
	manager, err := lightclients.NewLightClientManager(0, liteCtx, liteConf, engine)
	if err != nil {
		return nil, err
	}
	_, api := light.Bind(manager.Clients())
	sess := &session{
		mode:    ModePro,
		shards:  append([]uint(nil), shards...),
		dataDir: dataDir,
		api:     api,
		full:    make(map[uint]*scdo.ScdoService, len(shards)),
		samples: map[uint]heightSample{},
	}
	primary := shards[0]
	cacheMB := light.ProChainCacheMB(len(shards))
	var started []*node.Node
	fail := func(err error) (*session, error) {
		for i := len(started) - 1; i >= 0; i-- {
			started[i].Stop()
		}
		// Header clients are closed by the first node's Stop once that node
		// has been started. Before that, close them here.
		if len(started) == 0 {
			for _, client := range manager.Clients() {
				if client != nil {
					client.Stop()
				}
			}
		}
		return nil, err
	}
	for i, shard := range shards {
		dir := ProShardDir(dataDir, shard)
		conf, err := baseConfig(dir)
		if err != nil {
			return fail(err)
		}
		conf.BasicConfig.Name = fmt.Sprintf("SCDO Pro %d", shard)
		conf.BasicConfig.DbCache = cacheMB
		conf.ScdoConfig.GenesisConfig.ShardNumber = shard
		conf.P2PConfig.ListenAddr = proListenAddr(shard)
		conf.HTTPServer.HTTPAddr = proRPCAddr(shard, primary)
		conf.BasicConfig.RPCAddr = ""
		conf.IpcConfig.PipeName = ""
		conf.WSServerConfig.Address = ""
		if err = loadP2PKey(&conf.P2PConfig, dir); err != nil {
			return fail(err)
		}
		shardEngine, err := factory.GetConsensusEngine(common.ZpowAlgorithm)
		if err != nil {
			return fail(err)
		}
		n, err := node.New(conf)
		if err != nil {
			return fail(err)
		}
		if i == 0 {
			for _, svc := range manager.GetServices() {
				if err = n.Register(svc); err != nil {
					return fail(err)
				}
			}
			if err = n.Register(light.NewMultiService(manager.Clients())); err != nil {
				return fail(err)
			}
		}
		full, err := scdo.NewScdoService(serviceContext(dir), conf, log.GetLogger(fmt.Sprintf("scdo-%d", shard)), shardEngine, manager, -1, false)
		if err != nil {
			return fail(err)
		}
		if err = n.Register(full); err != nil {
			full.Stop()
			return fail(err)
		}
		if err = n.Start(); err != nil {
			started = append(started, n)
			return fail(err)
		}
		started = append(started, n)
		sess.full[shard] = full
	}
	sess.nodes = started
	return sess, nil
}

func serviceContext(dataDir string) context.Context {
	return context.WithValue(context.Background(), "ServiceContext", scdo.ServiceContext{DataDir: dataDir})
}
