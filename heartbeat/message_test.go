/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package heartbeat

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
)

func TestCanonicalJSONSortsKeysAndOmitsSig(t *testing.T) {
	msg := Message{
		V:             Version,
		NodeID:        "0x04aa",
		Kind:          KindFull,
		PayoutAddress: "0x1111111111111111111111111111111111111111",
		Client:        ClientGoSCDO,
		TS:            1791430000,
		Sig:           "0xshould-not-appear",
		Shards: []Shard{
			{Shard: 4, Height: 10, HeadHash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			{Shard: 1, Height: 3412345, HeadHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}
	got, err := CanonicalJSON(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"client":"go-scdo/2.0.0","kind":"full","node_id":"0x04aa","payout_address":"0x1111111111111111111111111111111111111111","shards":[{"head_hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","height":3412345,"shard":1},{"head_hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","height":10,"shard":4}],"ts":1791430000,"v":1}`
	if string(got) != want {
		t.Fatalf("canonical\n got %s\nwant %s", got, want)
	}
	if bytes.Contains(got, []byte(" ")) || bytes.Contains(got, []byte("sig")) {
		t.Fatalf("canonical contains a space or sig: %s", got)
	}
	again, err := CanonicalJSON(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("canonical JSON is not stable")
	}
}

func TestCanonicalJSONEscapesStrings(t *testing.T) {
	msg := Message{V: 1, Kind: KindLight, Client: `a"b`, NodeID: "0x04", PayoutAddress: "0x22", TS: 1}
	got, err := CanonicalJSON(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"a\"b"`)) {
		t.Fatalf("string was not escaped: %s", got)
	}
}

func TestSignatureRecoversNodeKey(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := NodeID(key)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{
		V:             Version,
		NodeID:        nodeID,
		Kind:          KindLight,
		PayoutAddress: "0x2222222222222222222222222222222222222222",
		Client:        ClientMobile,
		TS:            1791430000,
		Shards:        []Shard{{Shard: 2, Height: 9, HeadHash: "0x" + strings.Repeat("ab", 32)}},
	}
	canonical, err := CanonicalJSON(msg)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignNodeKey(key, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig length %d", len(sig))
	}
	recovered, err := crypto.Ecrecover(crypto.Keccak256(canonical), sig)
	if err != nil {
		t.Fatal(err)
	}
	if hexutil.BytesToHex(recovered) != nodeID {
		t.Fatalf("recovered %s node %s", hexutil.BytesToHex(recovered), nodeID)
	}
	wire := AttachSig(canonical, sig)
	if !bytes.Contains(wire, []byte(`"sig":"`)) {
		t.Fatalf("wire body has no sig: %s", wire)
	}
	other, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := NodeID(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherID == nodeID {
		t.Fatal("two node keys produced one id")
	}
}

func TestShardsFromTipsPreferFullHead(t *testing.T) {
	lightHash := common.BytesToHash([]byte{1})
	fullHash := common.BytesToHash([]byte{2})
	tips := []Tip{
		{Shard: 3, Height: 4, Hash: lightHash},
		{Shard: 1, Height: 8, Hash: lightHash},
		{Shard: 1, Height: 9, Hash: fullHash, Full: true},
		{Shard: 2, Height: 1, Hash: common.Hash{}},
	}
	got := ShardsFromTips(tips)
	if len(got) != 2 {
		t.Fatalf("shards %+v", got)
	}
	if got[0].Shard != 1 || got[0].Height != 9 || got[0].HeadHash != fullHash.Hex() {
		t.Fatalf("full head lost: %+v", got[0])
	}
	if got[1].Shard != 3 {
		t.Fatalf("order %+v", got)
	}
}

func TestNormalizeAddressAndURL(t *testing.T) {
	got, err := NormalizeAddress("ABCDEF0000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0xabcdef0000000000000000000000000000000001" {
		t.Fatal(got)
	}
	if _, err = NormalizeAddress("0x1234"); err == nil {
		t.Fatal("short address accepted")
	}
	empty, err := NormalizeAddress("  ")
	if err != nil || empty != "" {
		t.Fatalf("empty: %q %v", empty, err)
	}
	if !Enabled(got, "https://scdoscan.io/nodes/api/heartbeat") {
		t.Fatal("enabled pair reported off")
	}
	if Enabled(got, "") || Enabled("", "https://scdoscan.io/nodes/api/heartbeat") {
		t.Fatal("partial config reported on")
	}
	if _, err = NormalizeURL("ftp://example.com/h"); err == nil {
		t.Fatal("ftp accepted")
	}
	if _, err = NormalizeURL("https://"); err == nil {
		t.Fatal("bare https accepted")
	}
	url, err := NormalizeURL(" https://example.com/nodes/api/heartbeat ")
	if err != nil || url != "https://example.com/nodes/api/heartbeat" {
		t.Fatalf("url %q %v", url, err)
	}
}
