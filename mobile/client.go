// Package mobile is the SCDO Classic node embedded in an Android wallet.
//
// gomobile bind -target=android -o scdo.aar github.com/scdoproject/go-scdo/mobile
//
// The bind needs a C compiler because the node id uses libsecp256k1.
//
// Start(dataDir, mode, shards) runs one of two modes. Lite header-syncs the
// selected shards from fork genesis and checks every header with ZPoW. There
// is no snapshot. After a header checks out, older ones are pruned down to
// the last 10_000 per shard. A hash accumulator keeps older transaction
// proofs checkable. Pro downloads full blocks and state for the selected
// shards and validates them from fork genesis, again with no snapshot.
// Switching lite to pro leaves the verified header databases in place.
//
// The default sync policy allows a metered network (5G) and pauses on a low
// battery. The app reports that state through SetDeviceState.
//
// The process serves JSON-RPC on 127.0.0.1:18037 under the light namespace.
// See docs/mobile-light-client.md.
package mobile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
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
	mode    string
	shards  []uint
	dataDir string
	nodes   []*node.Node
	api     *light.PublicAPI
	full    map[uint]*scdo.ScdoService
	samples map[uint]heightSample
}

func (s *session) stop() {
	if s == nil {
		return
	}
	for i := len(s.nodes) - 1; i >= 0; i-- {
		if s.nodes[i] != nil {
			s.nodes[i].Stop()
		}
	}
}

// Start runs lite or pro for the selected Classic shards.
// mode is "lite" or "pro". shards is "1,2,3,4" (empty means all four).
// A relative dataDir is placed under $HOME/.scdo. An empty dataDir uses $HOME/.scdo.
// Calling Start again after Stop resumes from the databases already on disk.
// Pro does not delete the lite header databases.
func Start(dataDir, mode, shards string) error {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return fmt.Errorf("node is already started")
	}
	parsedMode, err := ParseMode(mode)
	if err != nil {
		return err
	}
	parsedShards, err := ParseShards(shards)
	if err != nil {
		return err
	}
	dataDir = common.ResolveDataDir(dataDir)
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	metered, battery := DefaultSyncPolicy()
	light.SetSyncPolicy(metered, battery)
	scdo.ExternalSyncPause = light.SyncPause

	var sess *session
	if parsedMode == ModePro {
		sess, err = startPro(dataDir, parsedShards)
	} else {
		sess, err = startLite(dataDir, parsedShards)
	}
	if err != nil {
		return err
	}
	current = sess
	return nil
}

// Stop shuts the node down. Header and full databases stay on disk.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return
	}
	current.stop()
	current = nil
}

// Status returns a JSON array, one object per running shard: height, peer
// height, peers, network, ETA, and disk used.
func Status() string {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return jsonErr("node is not started")
	}
	return jsonOK(current.statuses())
}

// Syncing is Status. The wallet can call either name.
func Syncing() string {
	return Status()
}

// Balance returns the JSON account proof for address. The proof is checked
// against the head header of that address's shard.
func Balance(address string) string {
	api := api()
	if api == nil {
		return jsonErr("node is not started")
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
		return jsonErr("node is not started")
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
		return jsonErr("node is not started")
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

// Pause stops downloads. The verified tip stays on disk.
func Pause(reason string) error {
	light.Pause(reason)
	cancelFullDownloads()
	return nil
}

// Resume clears a manual pause. A low battery can still hold sync.
func Resume() error {
	light.Resume()
	return nil
}

// SetSyncPolicy chooses whether a metered network or a low battery pauses sync.
// The Start default is metered=false (5G allowed) and low battery=true.
func SetSyncPolicy(pauseOnMetered, pauseOnLowBattery bool) error {
	light.SetSyncPolicy(pauseOnMetered, pauseOnLowBattery)
	if paused, _ := light.SyncPause(); paused {
		cancelFullDownloads()
	}
	return nil
}

// SetDeviceState reports the phone's network and battery. Go cannot read those itself.
func SetDeviceState(metered, lowBattery bool) error {
	light.SetDeviceState(metered, lowBattery)
	if paused, _ := light.SyncPause(); paused {
		cancelFullDownloads()
	}
	return nil
}

func cancelFullDownloads() {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return
	}
	for _, svc := range current.full {
		if svc == nil || svc.Downloader() == nil {
			continue
		}
		svc.Downloader().Cancel()
	}
}

// VerifyHeader checks headerHex (RLP hex) and siblingsJSON (hex hash array)
// against the accumulator this phone built. shard is 1-4.
func VerifyHeader(shard int, headerHex, siblingsJSON string) string {
	api := api()
	if api == nil {
		return jsonErr("node is not started")
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
		return jsonErr("node is not started")
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
