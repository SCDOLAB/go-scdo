// Pectra-era precompiled contracts: EIP-2935 (history block hashes) at 0x0F
// and EIP-2537 (BLS12-381) at 0x09-0x0E.
//
// For BLS12-381, the precompile addresses are reserved and gas is charged
// per the EIP schedule; Run returns empty output until the real cryptographic
// backend is linked in. This keeps address space stable for future upgrades.

package vm

import (
	"math/big"

	"github.com/scdoproject/go-scdo/common"
)

// historyStorageAddress is the precompile address for EIP-2935.
var historyStorageAddress = common.BytesToAddress([]byte{0x0f})

// historyStorageGas is the flat gas cost for EIP-2935 lookup.
const historyStorageGas = 300

// historyStorage implements EIP-2935: given a 32-byte block number on the
// stack, returns the corresponding block hash via the EVM GetHash function.
// It is implemented as a closure so it can capture GetHash.
type historyStorage struct {
	getHash func(uint64) common.Hash
}

func (c *historyStorage) RequiredGas(input []byte) uint64 {
	return historyStorageGas
}

func (c *historyStorage) Run(input []byte) ([]byte, error) {
	if len(input) < 32 {
		input = common.RightPadBytes(input, 32)
	}
	num := new(big.Int).SetBytes(input[:32])
	if !num.IsUint64() {
		return make([]byte, 32), nil
	}
	h := c.getHash(num.Uint64())
	return h.Bytes(), nil
}

// --- EIP-2537 BLS12-381 precompile addresses (stub) ---

type bls12381G1Add struct{}

func (c *bls12381G1Add) RequiredGas(input []byte) uint64 { return 375 }
func (c *bls12381G1Add) Run(input []byte) ([]byte, error) {
	return make([]byte, 128), nil
}

type bls12381G2Add struct{}

func (c *bls12381G2Add) RequiredGas(input []byte) uint64 { return 1375 }
func (c *bls12381G2Add) Run(input []byte) ([]byte, error) {
	return make([]byte, 256), nil
}

type bls12381G1Mul struct{}

func (c *bls12381G1Mul) RequiredGas(input []byte) uint64 { return 12000 }
func (c *bls12381G1Mul) Run(input []byte) ([]byte, error) {
	return make([]byte, 128), nil
}

type bls12381G2Mul struct{}

func (c *bls12381G2Mul) RequiredGas(input []byte) uint64 { return 22500 }
func (c *bls12381G2Mul) Run(input []byte) ([]byte, error) {
	return make([]byte, 256), nil
}

type bls12381Pairing struct{}

func (c *bls12381Pairing) RequiredGas(input []byte) uint64 {
	return 113000 + uint64(len(input)/384)*55000
}
func (c *bls12381Pairing) Run(input []byte) ([]byte, error) {
	return make([]byte, 32), nil
}

type bls12381MapG1 struct{}

func (c *bls12381MapG1) RequiredGas(input []byte) uint64 { return 5500 }
func (c *bls12381MapG1) Run(input []byte) ([]byte, error) {
	return make([]byte, 128), nil
}

type bls12381MapG2 struct{}

func (c *bls12381MapG2) RequiredGas(input []byte) uint64 { return 23800 }
func (c *bls12381MapG2) Run(input []byte) ([]byte, error) {
	return make([]byte, 256), nil
}

// PrecompiledContractsPectra includes all Byzantium precompiles plus the
// Pectra-era additions (BLS12-381 at 0x09-0x0e; EIP-2935 history at 0x0f
// is handled specially in run() because it needs GetHash).
var PrecompiledContractsPectra = map[common.Address]PrecompiledContract{
	common.BytesToAddress([]byte{1}):  &ecrecover{},
	common.BytesToAddress([]byte{2}):  &sha256hash{},
	common.BytesToAddress([]byte{3}):  &ripemd160hash{},
	common.BytesToAddress([]byte{4}):  &dataCopy{},
	common.BytesToAddress([]byte{5}):  &bigModExp{},
	common.BytesToAddress([]byte{6}):  &bn256Add{},
	common.BytesToAddress([]byte{7}):  &bn256ScalarMul{},
	common.BytesToAddress([]byte{8}):  &bn256Pairing{},
	common.BytesToAddress([]byte{9}):  &bls12381G1Add{},
	common.BytesToAddress([]byte{10}): &bls12381G2Add{},
	common.BytesToAddress([]byte{11}): &bls12381G1Mul{},
	common.BytesToAddress([]byte{12}): &bls12381G2Mul{},
	common.BytesToAddress([]byte{13}): &bls12381Pairing{},
	common.BytesToAddress([]byte{14}): &bls12381MapG1{},
	common.BytesToAddress([]byte{15}): &bls12381MapG2{},
}
