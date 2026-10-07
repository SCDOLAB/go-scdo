/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package mobile

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/light"
	"github.com/scdoproject/go-scdo/trie"
)

func TestCallsBeforeStart(t *testing.T) {
	if !strings.Contains(Syncing(), "light node is not started") {
		t.Fatalf("syncing: %s", Syncing())
	}
	if !strings.Contains(Balance("1S01dfdbe4d921d507032cb83ee04bb7efc4fd9a51"), "error") {
		t.Fatal(Balance("1S01dfdbe4d921d507032cb83ee04bb7efc4fd9a51"))
	}
	if !strings.Contains(TxProof("0x11"), "error") {
		t.Fatal(TxProof("0x11"))
	}
	if !strings.Contains(DebtProof("0x11"), "error") {
		t.Fatal(DebtProof("0x11"))
	}
}

func TestEstimateJSON(t *testing.T) {
	body := Estimate(0)
	var est light.SyncEstimate
	if err := json.Unmarshal([]byte(body), &est); err != nil {
		t.Fatal(err, body)
	}
	if est.Shards != 4 || est.ForkGenesis != common.ScdoForkHeight {
		t.Fatalf("%+v", est)
	}
	if est.PhoneStorageBytes == 0 || est.DownloadBytes == 0 {
		t.Fatalf("%+v", est)
	}
	if est.PhoneStorageBytes >= 1<<30 {
		t.Fatalf("phone storage %d", est.PhoneStorageBytes)
	}
	if est.DownloadBytes < 4<<30 {
		t.Fatalf("download %d", est.DownloadBytes)
	}
	if est.RetainedPerShard != light.RetainedHeaders {
		t.Fatalf("%+v", est)
	}
}

func TestSyncPolicyPausesOnMetered(t *testing.T) {
	light.Resume()
	light.SetSyncPolicy(false, false)
	light.SetDeviceState(false, false)
	t.Cleanup(func() {
		light.Resume()
		light.SetSyncPolicy(false, false)
		light.SetDeviceState(false, false)
	})
	if err := SetSyncPolicy(true, true); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceState(true, false); err != nil {
		t.Fatal(err)
	}
	paused, reason := light.SyncPause()
	if !paused || reason != "metered" {
		t.Fatalf("paused=%v reason=%s", paused, reason)
	}
	if err := SetDeviceState(false, true); err != nil {
		t.Fatal(err)
	}
	paused, reason = light.SyncPause()
	if !paused || reason != "low-battery" {
		t.Fatalf("paused=%v reason=%s", paused, reason)
	}
	if err := Resume(); err != nil {
		t.Fatal(err)
	}
	paused, _ = light.SyncPause()
	if !paused {
		t.Fatal("low battery should still pause")
	}
	if err := SetDeviceState(false, false); err != nil {
		t.Fatal(err)
	}
	paused, _ = light.SyncPause()
	if paused {
		t.Fatal("expected sync to resume")
	}
	if !strings.Contains(VerifyHeader(1, "0x", "[]"), "light node is not started") {
		t.Fatal(VerifyHeader(1, "0x", "[]"))
	}
}

func TestVerifyAccountProof(t *testing.T) {
	addr, _ := crypto.MustGenerateShardKeyPair(1)
	key := append(crypto.MustHash(*addr).Bytes(), byte('0'))
	leaf, err := common.Serialize(struct {
		Nonce    uint64
		Amount   *big.Int
		CodeHash []byte
	}{Nonce: 2, Amount: big.NewInt(15), CodeHash: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	db := trie.NewEmptyTrie(nil, nil)
	if err = db.Put(key, leaf); err != nil {
		t.Fatal(err)
	}
	raw, err := db.GetProof(key)
	if err != nil {
		t.Fatal(err)
	}
	nodes := make([]light.ProofNode, 0, len(raw))
	for k, v := range raw {
		nodes = append(nodes, light.ProofNode{Hash: hexutil.BytesToHex([]byte(k)), Bytes: hexutil.BytesToHex(v)})
	}
	encoded, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	body := VerifyAccount(db.Hash().Hex(), hexutil.BytesToHex(key), string(encoded))
	if strings.Contains(body, "error") {
		t.Fatal(body)
	}
	if !strings.Contains(body, `"balance":"15"`) || !strings.Contains(body, `"included":true`) {
		t.Fatal(body)
	}
}
