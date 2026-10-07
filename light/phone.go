/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"context"
	"fmt"
	"math/big"
	"path/filepath"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/consensus"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/rpc"
)

// ShardStatus is one shard's header sync, as reported to a phone wallet.
type ShardStatus struct {
	Shard       uint   `json:"shard"`
	Height      uint64 `json:"height"`
	PeerHeight  uint64 `json:"peerHeight"`
	Peers       int    `json:"peers"`
	Syncing     bool   `json:"syncing"`
	Mode        string `json:"mode"`
	ForkGenesis uint64 `json:"forkGenesis"`
}

// Status reports this shard's header tip. Mode is always headers.
func (s *ServiceClient) Status() ShardStatus {
	st := ShardStatus{
		Mode:        "headers",
		ForkGenesis: common.ScdoForkHeight,
	}
	if s == nil {
		return st
	}
	st.Shard = s.shard
	if s.chain != nil && s.chain.CurrentHeader() != nil {
		st.Height = s.chain.CurrentHeader().Height
	}
	if s.scdoProtocol == nil {
		return st
	}
	if s.scdoProtocol.downloader != nil {
		st.Syncing = s.scdoProtocol.downloader.syncStatus == statusDownloading
	}
	if s.scdoProtocol.peerSet != nil {
		st.Peers = len(s.scdoProtocol.peerSet.getPeers())
		if best := s.scdoProtocol.peerSet.bestPeer(); best != nil {
			st.PeerHeight = best.HeadHeight()
		}
	}
	return st
}

// OpenShards builds a header-only client for every Classic shard. Each client
// stores headers from fork genesis and checks them with the consensus engine.
// Only the home shard publishes the scdo RPC namespace. Callers register every
// client plus NewMultiService.
func OpenShards(ctx context.Context, conf *node.Config, engine consensus.Engine) ([]*ServiceClient, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}
	home := conf.ScdoConfig.GenesisConfig.ShardNumber
	if home == 0 {
		home = common.LocalShardNumber
	}
	if home == 0 {
		home = 1
	}
	clients := make([]*ServiceClient, common.ShardCount+1)
	for shard := uint(1); shard <= uint(common.ShardCount); shard++ {
		copyConf := conf.Clone()
		copyConf.ScdoConfig.GenesisConfig.ShardNumber = shard
		folder := filepath.Join("db", fmt.Sprintf("lightchainforshard_%d", shard))
		client, err := NewServiceClient(ctx, copyConf, log.GetLogger(fmt.Sprintf("lightclient_%d", shard)), folder, shard, engine)
		if err != nil {
			for _, started := range clients {
				if started != nil {
					started.Stop()
				}
			}
			return nil, err
		}
		client.publishAPI = shard == home
		clients[shard] = client
	}
	return clients, nil
}

// PublicAPI is the light RPC namespace for a wallet on the phone.
type PublicAPI struct {
	clients []*ServiceClient
}

// NewPublicAPI creates the wallet API over one client per shard. Index 0 is unused.
func NewPublicAPI(clients []*ServiceClient) *PublicAPI {
	return &PublicAPI{clients: clients}
}

// Syncing returns header progress for every shard this process opened.
func (api *PublicAPI) Syncing() ([]ShardStatus, error) {
	if api == nil {
		return nil, fmt.Errorf("light node is not started")
	}
	out := make([]ShardStatus, 0, common.ShardCount)
	for shard := uint(1); shard <= uint(common.ShardCount); shard++ {
		client := api.client(shard)
		if client == nil {
			continue
		}
		out = append(out, client.Status())
	}
	return out, nil
}

// GetBalance returns the verified account proof for the address's own shard.
func (api *PublicAPI) GetBalance(account common.Address) (*AccountProof, error) {
	client, err := api.clientFor(account.Shard())
	if err != nil {
		return nil, err
	}
	return client.AccountProof(account)
}

// GetTxProof asks every shard and returns the first proof. A transaction hash
// does not say which shard packed it.
func (api *PublicAPI) GetTxProof(txHash string) (*TxProof, error) {
	hash, err := common.HexToHash(txHash)
	if err != nil {
		return nil, err
	}
	return firstProof(api, func(client *ServiceClient) (*TxProof, error) {
		return client.TxProof(hash)
	})
}

// GetDebtProof asks every shard and returns the first cross-shard debt proof.
func (api *PublicAPI) GetDebtProof(debtHash string) (*DebtProof, error) {
	hash, err := common.HexToHash(debtHash)
	if err != nil {
		return nil, err
	}
	return firstProof(api, func(client *ServiceClient) (*DebtProof, error) {
		return client.DebtProof(hash)
	})
}

// Estimate sizes header-only sync of all four shards up to head.
// head 0 uses the 2026-10-07 public-network sample.
func (api *PublicAPI) Estimate(head uint64) (*SyncEstimate, error) {
	headerBytes, diskBytes := sampleHeaderSizes()
	est := EstimateSync(head, headerBytes, diskBytes)
	return &est, nil
}

func (api *PublicAPI) client(shard uint) *ServiceClient {
	if api == nil || int(shard) >= len(api.clients) {
		return nil
	}
	return api.clients[shard]
}

func (api *PublicAPI) clientFor(shard uint) (*ServiceClient, error) {
	client := api.client(shard)
	if client == nil {
		return nil, fmt.Errorf("shard %d is not header-syncing", shard)
	}
	return client, nil
}

func firstProof[T any](api *PublicAPI, fn func(*ServiceClient) (T, error)) (T, error) {
	type got struct {
		v   T
		err error
	}
	var zero T
	if api == nil {
		return zero, fmt.Errorf("light node is not started")
	}
	ch := make(chan got, common.ShardCount)
	n := 0
	for shard := uint(1); shard <= uint(common.ShardCount); shard++ {
		client := api.client(shard)
		if client == nil {
			continue
		}
		n++
		go func(c *ServiceClient) {
			v, err := fn(c)
			ch <- got{v, err}
		}(client)
	}
	if n == 0 {
		return zero, fmt.Errorf("light node is not started")
	}
	var last error
	for i := 0; i < n; i++ {
		item := <-ch
		if item.err == nil {
			return item.v, nil
		}
		last = item.err
	}
	if last == nil {
		last = fmt.Errorf("not found")
	}
	return zero, last
}

// multiService exposes PublicAPI and does not open its own p2p protocol.
type multiService struct {
	api *PublicAPI
}

// Bind returns the light RPC service and the same API object for in-process calls.
func Bind(clients []*ServiceClient) (node.Service, *PublicAPI) {
	api := NewPublicAPI(clients)
	return &multiService{api: api}, api
}

// NewMultiService is the node.Service that publishes the light RPC namespace.
func NewMultiService(clients []*ServiceClient) node.Service {
	service, _ := Bind(clients)
	return service
}

func (m *multiService) Protocols() []p2p.Protocol { return nil }

func (m *multiService) Start(server *p2p.Server) error { return nil }

func (m *multiService) Stop() error { return nil }

func (m *multiService) APIs() []rpc.API {
	return []rpc.API{{
		Namespace: "light",
		Version:   "1.0",
		Service:   m.api,
		Public:    true,
	}}
}

func sampleHeaderSizes() (headerBytes, diskBytes int) {
	header := sampleClassicHeader()
	raw := common.SerializePanic(header)
	td, _ := new(big.Int).SetString("56442844525367", 10)
	return len(raw), HeaderDiskBytes(header, td)
}

// sampleClassicHeader matches a shard-1 header at height 9275180 retrieved
// from a public node on 2026-10-07. It is a size sample, not a checkpoint.
func sampleClassicHeader() *types.BlockHeader {
	creator, err := common.HexToAddress("1S01dfdbe4d921d507032cb83ee04bb7efc4fd9a51")
	if err != nil {
		creator = common.EmptyAddress
	}
	return &types.BlockHeader{
		PreviousBlockHash: common.MustHexToHash("0x4356da2a0ffeaf8b57ddd75982764a07128eae40a8e1991aff3f9a92925e29ee"),
		Creator:           creator,
		StateHash:         common.MustHexToHash("0xef1e70d2e09da93d8ec75aee3a0d622a16d52fce292be29bef3d5a35f9236140"),
		TxHash:            common.MustHexToHash("0xf52b1c41ad0897e28e1f65f72e37fee0c2d48bd8b1f85c0c331ed4e3fdc1f89d"),
		ReceiptHash:       common.MustHexToHash("0x7663b648e222755d7fa105edefb54258a6c78e1635105f002202e648bb97f0af"),
		TxDebtHash:        common.EmptyHash,
		DebtHash:          common.EmptyHash,
		Difficulty:        big.NewInt(2916692),
		Height:            SampledClassicHead,
		CreateTimestamp:   big.NewInt(1791391718),
		Witness:           []byte("14759500041359592977"),
		Consensus:         types.PowConsensus,
	}
}
