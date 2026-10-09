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
	Paused      bool   `json:"paused"`
	PauseReason string `json:"pauseReason,omitempty"`
	Network     string `json:"network"`
	ETA         string `json:"eta"`
	DiskBytes   uint64 `json:"diskBytes"`
	// HeaderDiskBytes is the verified header database. Pro mode leaves it in
	// place when the full shard database is separate.
	HeaderDiskBytes uint64 `json:"headerDiskBytes,omitempty"`
	Retained        uint64 `json:"retained"`
	MMRLeaves       uint64 `json:"mmrLeaves"`
}

// Status reports this shard's header tip. Mode is always headers.
func (s *ServiceClient) Status() ShardStatus {
	paused, reason := SyncPause()
	st := ShardStatus{
		Mode:        "headers",
		ForkGenesis: common.ScdoForkHeight,
		Paused:      paused,
		PauseReason: reason,
		Network:     NetworkLabel(),
		ETA:         "unknown",
		Retained:    RetainedHeaders,
	}
	if s == nil {
		return st
	}
	st.Shard = s.shard
	if s.chain != nil && s.chain.CurrentHeader() != nil {
		st.Height = s.chain.CurrentHeader().Height
		st.MMRLeaves = s.chain.MMRLeaves()
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
	return OpenShardSet(ctx, conf, engine, nil)
}

// OpenShardSet is OpenShards limited to shards. An empty list opens shards 1-4.
// The folder is db/lightchainforshard_N under the context data directory.
func OpenShardSet(ctx context.Context, conf *node.Config, engine consensus.Engine, shards []uint) ([]*ServiceClient, error) {
	if conf == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if len(shards) == 0 {
		for shard := uint(1); shard <= uint(common.ShardCount); shard++ {
			shards = append(shards, shard)
		}
	}
	home := shards[0]
	clients := make([]*ServiceClient, common.ShardCount+1)
	for _, shard := range shards {
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

// NetworkLabel is what the wallet should show for the sync gate.
// A metered network is allowed unless the policy says to pause, because
// the phone syncs on 5G in real time.
func NetworkLabel() string {
	paused, reason := SyncPause()
	if paused {
		if reason == "" {
			return "paused"
		}
		return "paused:" + reason
	}
	if DeviceMetered() {
		return "metered-allowed"
	}
	return "unmetered"
}

// PublicAPI is the light RPC namespace for a wallet on the phone.
type PublicAPI struct {
	clients []*ServiceClient
}

// NewPublicAPI creates the wallet API over one client per shard. Index 0 is unused.
func NewPublicAPI(clients []*ServiceClient) *PublicAPI {
	return &PublicAPI{clients: clients}
}

// ShardHead is the header tip of one shard this process opened.
type ShardHead struct {
	Shard  uint
	Height uint64
	Hash   common.Hash
}

// Heads returns the header tip of every shard this process opened.
func (api *PublicAPI) Heads() []ShardHead {
	if api == nil {
		return nil
	}
	out := make([]ShardHead, 0, len(api.clients))
	for _, client := range api.clients {
		if client == nil {
			continue
		}
		height, hash, ok := client.Head()
		if !ok {
			continue
		}
		out = append(out, ShardHead{Shard: client.Shard(), Height: height, Hash: hash})
	}
	return out
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
// head 0 uses the 2026-10-07 public-network sample. Storage is the pruned
// window plus the accumulator. Download is still every header from fork genesis.
func (api *PublicAPI) Estimate(head uint64) (*SyncEstimate, error) {
	headerBytes, diskBytes := sampleHeaderSizes()
	est := EstimateSync(head, headerBytes, diskBytes)
	return &est, nil
}

// Pause stops header downloads until Resume. The verified tip stays on disk.
func (api *PublicAPI) Pause(reason string) error {
	Pause(reason)
	return nil
}

// Resume clears a manual pause.
func (api *PublicAPI) Resume() error {
	Resume()
	return nil
}

// SetSyncPolicy opts into pausing on a metered network or a low battery.
func (api *PublicAPI) SetSyncPolicy(pauseOnMetered, pauseOnLowBattery bool) error {
	SetSyncPolicy(pauseOnMetered, pauseOnLowBattery)
	return nil
}

// SetDeviceState records what the phone app read from the OS. Go cannot see
// Android BatteryManager or the metered-network flag itself.
func (api *PublicAPI) SetDeviceState(metered, lowBattery bool) error {
	SetDeviceState(metered, lowBattery)
	return nil
}

// VerifyHeader checks a header and its MMR siblings against the accumulator
// this shard built while it verified the chain. The header bytes come from a
// full node's light_getHeaderProof.
func (api *PublicAPI) VerifyHeader(shard uint, headerHex string, siblings []string) error {
	_, _, err := api.verifiedHeader(shard, headerHex, siblings)
	return err
}

// VerifyTx checks a historical transaction. The header must verify against
// this phone's accumulator, and the trie proof must match that header's tx root.
func (api *PublicAPI) VerifyTx(shard uint, headerHex string, siblings []string, txHash string, proof []ProofNode) error {
	header, _, err := api.verifiedHeader(shard, headerHex, siblings)
	if err != nil {
		return err
	}
	hash, err := common.HexToHash(txHash)
	if err != nil {
		return err
	}
	value, err := VerifyTrieValue(header.TxHash, hash.Bytes(), proof)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return fmt.Errorf("transaction proof shows the transaction is absent")
	}
	return nil
}

// VerifyDebt checks a historical cross-shard debt against the same accumulator.
func (api *PublicAPI) VerifyDebt(shard uint, headerHex string, siblings []string, debtHash string, proof []ProofNode) error {
	header, _, err := api.verifiedHeader(shard, headerHex, siblings)
	if err != nil {
		return err
	}
	hash, err := common.HexToHash(debtHash)
	if err != nil {
		return err
	}
	value, err := VerifyTrieValue(header.DebtHash, hash.Bytes(), proof)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return fmt.Errorf("debt proof shows the debt is absent")
	}
	return nil
}

func (api *PublicAPI) verifiedHeader(shard uint, headerHex string, siblings []string) (*types.BlockHeader, []common.Hash, error) {
	client, err := api.clientFor(shard)
	if err != nil {
		return nil, nil, err
	}
	if client.chain == nil {
		return nil, nil, fmt.Errorf("shard %d header chain is not ready", shard)
	}
	header, err := headerFromHex(headerHex)
	if err != nil {
		return nil, nil, err
	}
	sibs, err := decodeHashes(siblings)
	if err != nil {
		return nil, nil, err
	}
	if err = client.chain.verifyCanonicalHeader(header, sibs); err != nil {
		return nil, nil, err
	}
	return header, sibs, nil
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
