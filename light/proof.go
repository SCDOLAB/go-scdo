/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	"math/big"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/common/hexutil"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/scdoproject/go-scdo/trie"
)

// provenAccount matches the RLP layout of a state account.
type provenAccount struct {
	Nonce    uint64
	Amount   *big.Int
	CodeHash []byte
}

// ProofNode is one Merkle node. Hash is the node key the verifier walks.
type ProofNode struct {
	Hash  string `json:"hash"`
	Bytes string `json:"bytes"`
}

// AccountProof is a balance at a header this node has already checked.
// Included is false when the proof shows the account is absent. Balance is
// then zero. That is not an error.
type AccountProof struct {
	Account    string      `json:"account"`
	Shard      uint        `json:"shard"`
	Balance    *big.Int    `json:"balance"`
	Nonce      uint64      `json:"nonce"`
	Included   bool        `json:"included"`
	StateRoot  string      `json:"stateRoot"`
	HeaderHash string      `json:"headerHash"`
	Height     uint64      `json:"height"`
	AccountKey string      `json:"accountKey"`
	Proof      []ProofNode `json:"proof"`
}

// TxProof is a transaction inclusion proof against a verified header's tx root.
// Included is false while the transaction is still only in the pool.
type TxProof struct {
	TxHash        string      `json:"txHash"`
	Shard         uint        `json:"shard"`
	Included      bool        `json:"included"`
	BlockHash     string      `json:"blockHash,omitempty"`
	BlockHeight   uint64      `json:"blockHeight,omitempty"`
	TxRoot        string      `json:"txRoot,omitempty"`
	Confirmations uint64      `json:"confirmations"`
	Confirmed     bool        `json:"confirmed"`
	Proof         []ProofNode `json:"proof,omitempty"`
}

// DebtProof is a cross-shard debt inclusion proof against a verified header.
// Confirmed means the debt is at least ConfirmedBlockNumber blocks behind
// that shard's head. The confirmation count is not a new consensus rule.
type DebtProof struct {
	DebtHash          string      `json:"debtHash"`
	Shard             uint        `json:"shard"`
	Included          bool        `json:"included"`
	BlockHash         string      `json:"blockHash,omitempty"`
	BlockHeight       uint64      `json:"blockHeight,omitempty"`
	DebtRoot          string      `json:"debtRoot,omitempty"`
	Confirmations     uint64      `json:"confirmations"`
	ConfirmationsNeed uint64      `json:"confirmationsNeed"`
	Confirmed         bool        `json:"confirmed"`
	Proof             []ProofNode `json:"proof,omitempty"`
}

func accountTrieKey(addr common.Address) []byte {
	return append(crypto.MustHash(addr).Bytes(), byte('0'))
}

func encodeProof(nodes []proofNode) []ProofNode {
	out := make([]ProofNode, len(nodes))
	for i, n := range nodes {
		out[i] = ProofNode{
			Hash:  hexutil.BytesToHex([]byte(n.Key)),
			Bytes: hexutil.BytesToHex(n.Value),
		}
	}
	return out
}

// VerifyTrieValue checks proof against root and returns the leaf bytes.
// A nil value with a nil error means the proof shows the key is absent.
func VerifyTrieValue(root common.Hash, key []byte, nodes []ProofNode) ([]byte, error) {
	proof := make(map[string][]byte, len(nodes))
	for _, n := range nodes {
		hash, err := hexutil.HexToBytes(n.Hash)
		if err != nil {
			return nil, errors.NewStackedError(err, "proof node hash is not hex")
		}
		value, err := hexutil.HexToBytes(n.Bytes)
		if err != nil {
			return nil, errors.NewStackedError(err, "proof node bytes are not hex")
		}
		proof[string(hash)] = value
	}
	value, err := trie.VerifyProof(root, key, proof)
	if err != nil {
		return nil, errors.NewStackedError(err, "merkle proof failed")
	}
	return value, nil
}

// DecodeAccount reads an account leaf. An empty value is a proof of absence
// and a zero balance.
func DecodeAccount(value []byte) (amount *big.Int, nonce uint64, included bool, err error) {
	if len(value) == 0 {
		return big.NewInt(0), 0, false, nil
	}
	var acc provenAccount
	if err = common.Deserialize(value, &acc); err != nil {
		return nil, 0, false, errors.NewStackedError(err, "account leaf is not an account")
	}
	if acc.Amount == nil {
		acc.Amount = big.NewInt(0)
	}
	return acc.Amount, acc.Nonce, true, nil
}

func confirmations(head, included uint64) (have uint64, confirmed bool) {
	if head > included {
		have = head - included
	}
	return have, have >= common.ConfirmedBlockNumber
}

// AccountProof loads the account with a state-trie proof against the head header.
func (s *ServiceClient) AccountProof(addr common.Address) (*AccountProof, error) {
	if s == nil || s.chain == nil || s.chain.CurrentHeader() == nil {
		return nil, errors.New("shard header is not ready")
	}
	header := s.chain.CurrentHeader()
	key := accountTrieKey(addr)
	response, err := s.odrBackend.retrieveWithFilter(&odrTriePoof{
		Root: header.StateHash,
		Key:  key,
	}, peerFilter{blockHash: header.Hash()})
	if err != nil {
		return nil, err
	}
	nodes := response.(*odrTriePoof).Proof
	value, err := trie.VerifyProof(header.StateHash, key, arrayToMap(nodes))
	if err != nil {
		return nil, errors.NewStackedError(err, "account merkle proof failed")
	}
	amount, nonce, included, err := DecodeAccount(value)
	if err != nil {
		return nil, err
	}
	return &AccountProof{
		Account:    addr.Hex(),
		Shard:      s.shard,
		Balance:    amount,
		Nonce:      nonce,
		Included:   included,
		StateRoot:  header.StateHash.Hex(),
		HeaderHash: header.Hash().Hex(),
		Height:     header.Height,
		AccountKey: hexutil.BytesToHex(key),
		Proof:      encodeProof(nodes),
	}, nil
}

// TxProof loads a transaction inclusion proof. The shard is the one this client syncs.
func (s *ServiceClient) TxProof(txHash common.Hash) (*TxProof, error) {
	if s == nil || s.chain == nil {
		return nil, errors.New("shard header is not ready")
	}
	response, err := s.odrBackend.retrieve(&odrTxByHashRequest{TxHash: txHash})
	if err != nil {
		return nil, err
	}
	result := response.(*odrTxByHashResponse)
	out := &TxProof{
		TxHash: result.TxHashHex(txHash),
		Shard:  s.shard,
		Proof:  encodeProof(result.Proof),
	}
	if result.Tx == nil && result.BlockIndex == nil {
		return nil, errors.New("transaction not found")
	}
	if result.BlockIndex == nil {
		return out, nil
	}
	header, err := s.chain.GetStore().GetBlockHeader(result.BlockIndex.BlockHash)
	if err != nil {
		return nil, errors.NewStackedError(err, "tx proof header is not stored")
	}
	out.Included = true
	out.BlockHash = result.BlockIndex.BlockHash.Hex()
	out.BlockHeight = result.BlockIndex.BlockHeight
	out.TxRoot = header.TxHash.Hex()
	head := uint64(0)
	if s.chain.CurrentHeader() != nil {
		head = s.chain.CurrentHeader().Height
	}
	out.Confirmations, out.Confirmed = confirmations(head, out.BlockHeight)
	return out, nil
}

// DebtProof loads a cross-shard debt inclusion proof on this shard.
func (s *ServiceClient) DebtProof(debtHash common.Hash) (*DebtProof, error) {
	if s == nil || s.chain == nil {
		return nil, errors.New("shard header is not ready")
	}
	response, err := s.odrBackend.retrieve(&odrDebtRequest{DebtHash: debtHash})
	if err != nil {
		return nil, err
	}
	result := response.(*odrDebtResponse)
	out := &DebtProof{
		DebtHash:          debtHash.Hex(),
		Shard:             s.shard,
		ConfirmationsNeed: common.ConfirmedBlockNumber,
		Proof:             encodeProof(result.Proof),
	}
	if result.Debt == nil && result.BlockIndex == nil {
		return nil, errors.New("debt not found")
	}
	if result.BlockIndex == nil {
		return out, nil
	}
	header, err := s.chain.GetStore().GetBlockHeader(result.BlockIndex.BlockHash)
	if err != nil {
		return nil, errors.NewStackedError(err, "debt proof header is not stored")
	}
	out.Included = true
	out.BlockHash = result.BlockIndex.BlockHash.Hex()
	out.BlockHeight = result.BlockIndex.BlockHeight
	out.DebtRoot = header.DebtHash.Hex()
	head := uint64(0)
	if s.chain.CurrentHeader() != nil {
		head = s.chain.CurrentHeader().Height
	}
	out.Confirmations, out.Confirmed = confirmations(head, out.BlockHeight)
	return out, nil
}

// TxHashHex keeps the requested hash when the body is still unpacked.
func (response *odrTxByHashResponse) TxHashHex(requested common.Hash) string {
	if response.Tx != nil && !response.Tx.Hash.IsEmpty() {
		return response.Tx.Hash.Hex()
	}
	return requested.Hex()
}
