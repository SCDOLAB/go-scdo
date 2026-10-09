/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"context"
	"path/filepath"

	"github.com/scdoproject/go-scdo/api"
	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/database"
	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/rpc"
	"github.com/scdoproject/go-scdo/scdo"
)

// ServiceClient implements service for light mode.
type ServiceClient struct {
	networkID    string
	netVersion   string
	p2pServer    *p2p.Server
	scdoProtocol *LightProtocol
	log          *log.ScdoLog
	odrBackend   *odrBackend

	txPool  *txPool
	chain   *LightChain
	lightDB database.Database // database used to store blocks and account state.

	shard uint

	// publishAPI is false for the extra shards of a phone node so the process
	// registers the scdo namespace once. The light namespace covers every shard.
	publishAPI bool
}

// NewServiceClient create ServiceClient. The header store is pruned to the
// retained window. Phones in lite mode and `node -l` use this.
func NewServiceClient(ctx context.Context, conf *node.Config, log *log.ScdoLog, dbFolder string, shard uint, engine consensus.Engine) (s *ServiceClient, err error) {
	return newServiceClient(ctx, conf, log, dbFolder, shard, engine, false)
}

// NewFullNodeHeaderClient is the other-shard header client used by a full
// node. It keeps every header from fork genesis so cross-shard debts can be
// checked while the local chain is still far behind the light tip.
func NewFullNodeHeaderClient(ctx context.Context, conf *node.Config, log *log.ScdoLog, dbFolder string, shard uint, engine consensus.Engine) (s *ServiceClient, err error) {
	s, err = newServiceClient(ctx, conf, log, dbFolder, shard, engine, true)
	if err == nil && s != nil && log != nil {
		log.Info("full node keeps every other-shard header for shard %d", shard)
	}
	return s, err
}

func newServiceClient(ctx context.Context, conf *node.Config, log *log.ScdoLog, dbFolder string, shard uint, engine consensus.Engine, retainAll bool) (s *ServiceClient, err error) {
	s = &ServiceClient{
		log:        log,
		networkID:  conf.P2PConfig.NetworkID,
		netVersion: conf.BasicConfig.Version,
		shard:      shard,
	}

	serviceContext := ctx.Value("ServiceContext").(scdo.ServiceContext)
	// Initialize blockchain DB.
	chainDBPath := filepath.Join(serviceContext.DataDir, dbFolder)
	log.Info("NewServiceClient BlockChain datadir is %s", chainDBPath)
	cacheMB := leveldb.AutoCacheMB(chainDBPath)
	// Four header databases share a phone. 32 MiB of block cache each is
	// enough for the retained window; the old 128–512 MiB cap is not.
	if cacheMB > 32 {
		cacheMB = 32
	}
	log.Info("light chain shard %d db cache %d MB", shard, cacheMB)
	s.lightDB, err = leveldb.NewLevelDBWithCache(chainDBPath, cacheMB)
	if err != nil {
		log.Error("NewServiceClient Create lightDB err. %s", err)
		return nil, err
	}

	bcStore := store.NewCachedStore(store.NewBlockchainDatabase(s.lightDB))
	s.odrBackend = newOdrBackend(bcStore, shard)
	// initialize and validate genesis
	genesis := core.GetGenesis(&conf.ScdoConfig.GenesisConfig)

	err = genesis.InitializeAndValidate(bcStore, s.lightDB)
	if err != nil {
		s.lightDB.Close()
		s.odrBackend.close()
		log.Error("light client genesis.Initialize err. %s", err)
		return nil, err
	}

	s.chain, err = openLightChain(bcStore, s.lightDB, s.odrBackend, engine, retainAll)
	if err != nil {
		s.lightDB.Close()
		s.odrBackend.close()
		log.Error("failed to init chain in light client. %s", err)
		return nil, err
	}

	s.txPool = newTxPool(s.chain, s.odrBackend, s.chain.headerChangedEventManager, s.chain.headRollbackEventManager)

	s.scdoProtocol, err = NewLightProtocol(conf.P2PConfig.NetworkID, s.txPool, nil, s.chain, false, s.odrBackend, log, shard)
	if err != nil {
		s.lightDB.Close()
		s.odrBackend.close()
		log.Error("failed to create protocol in light client, %s", err)
		return nil, err
	}

	s.odrBackend.start(s.scdoProtocol.peerSet) // start the odr backend
	s.publishAPI = true
	log.Info("light mode started.")
	return s, nil
}

// Shard is the shard this light client header-syncs.
func (s *ServiceClient) Shard() uint { return s.shard }

// CurrentHeight is the latest header height on this shard.
func (s *ServiceClient) CurrentHeight() uint64 {
	height, _, ok := s.Head()
	if !ok {
		return 0
	}
	return height
}

// Head is the canonical header this light client is serving.
func (s *ServiceClient) Head() (height uint64, hash common.Hash, ok bool) {
	if s == nil || s.chain == nil {
		return 0, common.EmptyHash, false
	}
	return s.chain.Head()
}

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *ServiceClient) Protocols() (protos []p2p.Protocol) {
	return append(protos, s.scdoProtocol.Protocol)
}

// Start implements node.Service, starting goroutines needed by ServiceClient.
func (s *ServiceClient) Start(srvr *p2p.Server) error {
	s.p2pServer = srvr
	s.scdoProtocol.p2pServer = srvr

	s.scdoProtocol.Start()
	return nil
}

// Stop implements node.Service, terminating all internal goroutines.
func (s *ServiceClient) Stop() error {
	// Protocol handlers exit before the ODR channel is shut and the database
	// is closed. Closing the database first left handlers sending on a closed
	// ODR channel during SIGTERM.
	if s.scdoProtocol != nil {
		s.scdoProtocol.Stop()
	}
	if s.odrBackend != nil {
		s.odrBackend.close()
	}
	if s.lightDB != nil {
		s.lightDB.Close()
	}
	return nil
}

// APIs implements node.Service, returning the collection of RPC services the scdo package offers.
func (s *ServiceClient) APIs() (apis []rpc.API) {
	if !s.publishAPI {
		return nil
	}
	return append(apis, api.GetAPIs(NewLightBackend(s))...)
}
