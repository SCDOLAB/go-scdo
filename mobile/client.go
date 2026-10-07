// Package mobile is the SCDO Classic light client for an Android wallet.
//
// gomobile bind -target=android -o scdo.aar github.com/scdoproject/go-scdo/mobile
//
// The bind needs a C compiler because the node id uses libsecp256k1.
// Start header-syncs shards 1-4 from fork genesis and checks every header
// with ZPoW. There is no snapshot. After a header checks out, older ones
// are pruned down to the last 10_000 per shard. A hash accumulator keeps
// older transaction proofs checkable. Balance, transaction and debt calls
// return Merkle proofs the node has already checked. Sync pauses when the
// app reports a metered network or a low battery.
//
// The same process also serves JSON-RPC on 127.0.0.1:18037 under the light
// namespace. See docs/mobile-light-client.md.
package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/consensus/factory"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/node"
	"github.com/scdoproject/go-scdo/p2p"
	"github.com/scdoproject/go-scdo/scdo"
)

const (
	// RPCAddress is the loopback JSON-RPC address started for the wallet.
	RPCAddress = "127.0.0.1:18037"

	// P2PAddress is the UDP/TCP port used to reach Classic peers.
	P2PAddress = "0.0.0.0:18057"
)

var (
	mu      sync.Mutex
	current *session
)

type session struct {
	n   *node.Node
	api *light.PublicAPI
}

// Start header-syncs all four Classic shards into dataDir.
// A relative dataDir is placed under $HOME/.scdo. An empty dataDir uses $HOME/.scdo.
func Start(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return fmt.Errorf("light node is already started")
	}
	dataDir = common.ResolveDataDir(dataDir)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	conf := &node.Config{}
	conf.BasicConfig.Name = "SCDO Light"
	conf.BasicConfig.Version = common.ScdoNodeVersion
	conf.BasicConfig.DataDir = dataDir
	conf.BasicConfig.MinerAlgorithm = common.ZpowAlgorithm
	conf.HTTPServer.HTTPAddr = RPCAddress
	conf.HTTPServer.HTTPCors = []string{"*"}
	conf.HTTPServer.HTTPWhiteHost = []string{"*"}
	conf.P2PConfig.NetworkID = "net1"
	conf.P2PConfig.ListenAddr = P2PAddress
	conf.ScdoConfig.GenesisConfig.ShardNumber = 1
	conf.ScdoConfig.GenesisConfig.Difficult = 1900000
	p2p.MergeBootnodes(&conf.P2PConfig)
	if err := loadP2PKey(&conf.P2PConfig, dataDir); err != nil {
		return err
	}

	engine, err := factory.GetConsensusEngine(common.ZpowAlgorithm)
	if err != nil {
		return err
	}
	ctx := context.WithValue(context.Background(), "ServiceContext", scdo.ServiceContext{DataDir: dataDir})
	n, err := node.New(conf)
	if err != nil {
		return err
	}
	clients, err := light.OpenShards(ctx, conf, engine)
	if err != nil {
		return err
	}
	for _, client := range clients {
		if client == nil {
			continue
		}
		if err = n.Register(client); err != nil {
			return err
		}
	}
	service, api := light.Bind(clients)
	if err = n.Register(service); err != nil {
		return err
	}
	// The desktop -l node leaves this off. A phone opts in, then reports
	// the OS state through SetDeviceState.
	light.SetSyncPolicy(true, true)
	if err = n.Start(); err != nil {
		return err
	}
	current = &session{n: n, api: api}
	return nil
}

// Stop shuts the light node down.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return
	}
	current.n.Stop()
	current = nil
}

// Syncing returns JSON header progress for shards 1-4.
func Syncing() string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	status, err := api.Syncing()
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(status)
}

// Balance returns the JSON account proof for address. The proof is checked
// against the head header of that address's shard.
func Balance(address string) string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	account, err := common.HexToAddress(address)
	if err != nil {
		return jsonErr(err.Error())
	}
	proof, err := api.GetBalance(account)
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(proof)
}

// TxProof returns the JSON transaction inclusion proof.
func TxProof(txHash string) string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	proof, err := api.GetTxProof(txHash)
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(proof)
}

// DebtProof returns the JSON cross-shard debt proof.
func DebtProof(debtHash string) string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	proof, err := api.GetDebtProof(debtHash)
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(proof)
}

// Estimate returns JSON storage and bandwidth for a header-only sync of all
// four shards up to head. head 0 uses the 2026-10-07 public height.
func Estimate(head int64) string {
	var height uint64
	if head > 0 {
		height = uint64(head)
	}
	est, err := (&light.PublicAPI{}).Estimate(height)
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(est)
}

// Pause stops header downloads. The verified tip stays on disk.
func Pause(reason string) error {
	light.Pause(reason)
	return nil
}

// Resume clears a manual pause. A metered network or low battery can still hold sync.
func Resume() error {
	light.Resume()
	return nil
}

// SetSyncPolicy chooses whether a metered network or a low battery pauses sync.
func SetSyncPolicy(pauseOnMetered, pauseOnLowBattery bool) error {
	light.SetSyncPolicy(pauseOnMetered, pauseOnLowBattery)
	return nil
}

// SetDeviceState reports the phone's network and battery. Go cannot read those itself.
func SetDeviceState(metered, lowBattery bool) error {
	light.SetDeviceState(metered, lowBattery)
	return nil
}

// VerifyHeader checks headerHex (RLP hex) and siblingsJSON (hex hash array)
// against the accumulator this phone built. shard is 1-4.
func VerifyHeader(shard int, headerHex, siblingsJSON string) string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	var siblings []string
	if err := json.Unmarshal([]byte(siblingsJSON), &siblings); err != nil {
		return jsonErr(err.Error())
	}
	if err := api.VerifyHeader(uint(shard), headerHex, siblings); err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(map[string]bool{"verified": true})
}

// VerifyTx checks a historical transaction against a header the phone's accumulator accepts.
func VerifyTx(shard int, headerHex, siblingsJSON, txHash, proofJSON string) string {
	return verifyTrie(shard, headerHex, siblingsJSON, txHash, proofJSON, true)
}

// VerifyDebt checks a historical cross-shard debt the same way.
func VerifyDebt(shard int, headerHex, siblingsJSON, debtHash, proofJSON string) string {
	return verifyTrie(shard, headerHex, siblingsJSON, debtHash, proofJSON, false)
}

func verifyTrie(shard int, headerHex, siblingsJSON, itemHash, proofJSON string, tx bool) string {
	api := api()
	if api == nil {
		return jsonErr("light node is not started")
	}
	var siblings []string
	if err := json.Unmarshal([]byte(siblingsJSON), &siblings); err != nil {
		return jsonErr(err.Error())
	}
	var nodes []light.ProofNode
	if err := json.Unmarshal([]byte(proofJSON), &nodes); err != nil {
		return jsonErr(err.Error())
	}
	var err error
	if tx {
		err = api.VerifyTx(uint(shard), headerHex, siblings, itemHash, nodes)
	} else {
		err = api.VerifyDebt(uint(shard), headerHex, siblings, itemHash, nodes)
	}
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(map[string]bool{"verified": true})
}

// VerifyAccount checks a balance proof without the network.
// proofJSON is the proof array from Balance. accountKey is the accountKey field.
func VerifyAccount(stateRoot, accountKey, proofJSON string) string {
	root, err := common.HexToHash(stateRoot)
	if err != nil {
		return jsonErr(err.Error())
	}
	key, err := hexutil.HexToBytes(accountKey)
	if err != nil {
		return jsonErr(err.Error())
	}
	var nodes []light.ProofNode
	if err = json.Unmarshal([]byte(proofJSON), &nodes); err != nil {
		return jsonErr(err.Error())
	}
	value, err := light.VerifyTrieValue(root, key, nodes)
	if err != nil {
		return jsonErr(err.Error())
	}
	amount, nonce, included, err := light.DecodeAccount(value)
	if err != nil {
		return jsonErr(err.Error())
	}
	return jsonOK(map[string]interface{}{
		"balance":  amount.String(),
		"nonce":    nonce,
		"included": included,
	})
}

func api() *light.PublicAPI {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return nil
	}
	return current.api
}

func jsonOK(v interface{}) string {
	body, err := json.Marshal(v)
	if err != nil {
		return jsonErr(err.Error())
	}
	return string(body)
}

func jsonErr(msg string) string {
	body, _ := json.Marshal(map[string]string{"error": msg})
	return string(body)
}

func loadP2PKey(cfg *p2p.Config, dataDir string) error {
	if cfg.PrivateKey != nil {
		return nil
	}
	keyPath := filepath.Join(dataDir, "p2p.key")
	if common.FileOrFolderExists(keyPath) {
		buff, err := os.ReadFile(keyPath)
		if err != nil {
			return err
		}
		key, err := crypto.LoadECDSAFromString(strings.TrimSpace(string(buff)))
		if err != nil {
			return err
		}
		cfg.PrivateKey = key
		return nil
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	encoded := hexutil.BytesToHex(crypto.FromECDSA(key))
	if err = common.SaveFile(keyPath, []byte(encoded+"\n")); err != nil {
		return err
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return err
	}
	cfg.PrivateKey = key
	return nil
}
