/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

// Package heartbeat posts a signed node sample to a configurable reward
// endpoint. It is off unless a payout address and a URL are both set.
// The node p2p key signs the sample. A wallet key is never used, and this
// package does not compute a reward.
package heartbeat

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
)

const (
	// Version is the heartbeat document version.
	Version = 1

	// KindLight is a process that only serves header chains.
	KindLight = "light"

	// KindFull is a process that serves at least one full chain.
	KindFull = "full"

	// Interval is the time between accepted samples.
	Interval = 300 * time.Second

	// Jitter is applied on either side of Interval and of a backoff wait.
	Jitter = 30 * time.Second

	// PostTimeout is the HTTP client timeout for one POST.
	PostTimeout = 10 * time.Second

	// BackoffBase is the first wait after a failed POST.
	BackoffBase = 30 * time.Second

	// BackoffCap is the longest wait after repeated failures.
	BackoffCap = 30 * time.Minute

	// ClientGoSCDO identifies the plain node and the desktop wallet's node process.
	ClientGoSCDO = "go-scdo/2.0.0"

	// ClientMobile identifies the gomobile phone node.
	ClientMobile = "scdo-mobile/0.1.10"
)

// Shard is one chain this process is serving.
type Shard struct {
	Shard    uint   `json:"shard"`
	Height   uint64 `json:"height"`
	HeadHash string `json:"head_hash"`
}

// Message is the heartbeat document. Sig is sent on the wire and omitted
// from the signed bytes.
type Message struct {
	V             int     `json:"v"`
	NodeID        string  `json:"node_id"`
	Kind          string  `json:"kind"`
	Shards        []Shard `json:"shards"`
	PayoutAddress string  `json:"payout_address"`
	Client        string  `json:"client"`
	TS            int64   `json:"ts"`
	Sig           string  `json:"sig,omitempty"`
}

// Tip is a chain head collected from a full block or a light header.
// Full is true when the head comes from a full chain. A shard reported
// both ways keeps the full head.
type Tip struct {
	Shard  uint
	Height uint64
	Hash   common.Hash
	Full   bool
}

// Enabled reports whether the client should post. Both values must be set.
func Enabled(address, url string) bool {
	return strings.TrimSpace(address) != "" && strings.TrimSpace(url) != ""
}

// NormalizeAddress accepts a 20-byte hex address, with or without 0x.
// An empty string clears the payout address. The result is lowercase 0x hex.
func NormalizeAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", nil
	}
	if !hexutil.Has0xPrefix(address) {
		address = "0x" + address
	}
	raw, err := hexutil.HexToBytes(address)
	if err != nil || len(raw) != common.AddressLen {
		return "", fmt.Errorf("reward address must be a 20-byte hex address")
	}
	return hexutil.BytesToHex(raw), nil
}

// NormalizeURL accepts an http or https URL. An empty string clears it.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
		return "", fmt.Errorf("reward heartbeat url must be an http or https URL")
	}
	if len(raw) <= len("https://") || strings.ContainsAny(raw[strings.Index(raw, "://")+3:], " \t") {
		return "", fmt.Errorf("reward heartbeat url must be an http or https URL")
	}
	host := raw[strings.Index(raw, "://")+3:]
	if host == "" || strings.HasPrefix(host, "/") {
		return "", fmt.Errorf("reward heartbeat url must be an http or https URL")
	}
	return raw, nil
}

// NodeID is the hex uncompressed secp256k1 public key of the node key.
func NodeID(key *ecdsa.PrivateKey) (string, error) {
	if key == nil {
		return "", fmt.Errorf("node key is missing")
	}
	pub := crypto.FromECDSAPub(&key.PublicKey)
	if len(pub) == 0 {
		return "", fmt.Errorf("node key has no public key")
	}
	return hexutil.BytesToHex(pub), nil
}

// CanonicalJSON is the signed encoding: object keys sorted, no spaces,
// and the sig field left out. Shard objects use the same rules.
func CanonicalJSON(msg Message) ([]byte, error) {
	if msg.Kind != KindLight && msg.Kind != KindFull {
		return nil, fmt.Errorf("kind must be light or full")
	}
	shards := append([]Shard(nil), msg.Shards...)
	sort.Slice(shards, func(i, j int) bool {
		if shards[i].Shard != shards[j].Shard {
			return shards[i].Shard < shards[j].Shard
		}
		return shards[i].HeadHash < shards[j].HeadHash
	})
	var buf bytes.Buffer
	buf.WriteString(`{"client":`)
	buf.Write(jsonString(msg.Client))
	buf.WriteString(`,"kind":`)
	buf.Write(jsonString(msg.Kind))
	buf.WriteString(`,"node_id":`)
	buf.Write(jsonString(msg.NodeID))
	buf.WriteString(`,"payout_address":`)
	buf.Write(jsonString(msg.PayoutAddress))
	buf.WriteString(`,"shards":[`)
	for i, shard := range shards {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"head_hash":`)
		buf.Write(jsonString(shard.HeadHash))
		buf.WriteString(`,"height":`)
		fmt.Fprintf(&buf, "%d", shard.Height)
		buf.WriteString(`,"shard":`)
		fmt.Fprintf(&buf, "%d", shard.Shard)
		buf.WriteByte('}')
	}
	buf.WriteString(`],"ts":`)
	fmt.Fprintf(&buf, "%d", msg.TS)
	buf.WriteString(`,"v":`)
	fmt.Fprintf(&buf, "%d", msg.V)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// SignNodeKey returns the 65-byte [R || S || V] signature of keccak256(canonical).
func SignNodeKey(key *ecdsa.PrivateKey, canonical []byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("node key is missing")
	}
	if len(canonical) == 0 {
		return nil, fmt.Errorf("empty heartbeat")
	}
	sig, err := crypto.Sign(key, crypto.Keccak256(canonical))
	if err != nil {
		return nil, err
	}
	if len(sig.Sig) != 65 {
		return nil, fmt.Errorf("signature is %d bytes", len(sig.Sig))
	}
	return sig.Sig, nil
}

// AttachSig appends the sig field to a canonical document.
func AttachSig(canonical []byte, sig []byte) []byte {
	out := make([]byte, 0, len(canonical)+len(sig)*2+16)
	out = append(out, canonical[:len(canonical)-1]...)
	out = append(out, []byte(`,"sig":`)...)
	out = append(out, jsonString(hexutil.BytesToHex(sig))...)
	out = append(out, '}')
	return out
}

// ShardsFromTips keeps one head per shard. A full head replaces a light head.
// A tip with an empty hash is skipped.
func ShardsFromTips(tips []Tip) []Shard {
	chosen := make(map[uint]Tip, len(tips))
	for _, tip := range tips {
		if tip.Hash == (common.Hash{}) {
			continue
		}
		prev, ok := chosen[tip.Shard]
		if ok && prev.Full && !tip.Full {
			continue
		}
		chosen[tip.Shard] = tip
	}
	out := make([]Shard, 0, len(chosen))
	for _, tip := range chosen {
		out = append(out, Shard{
			Shard:    tip.Shard,
			Height:   tip.Height,
			HeadHash: tip.Hash.Hex(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Shard < out[j].Shard })
	return out
}

func jsonString(s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		return []byte(`""`)
	}
	return b
}
