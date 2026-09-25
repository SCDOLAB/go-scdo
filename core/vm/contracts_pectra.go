// Pectra-era precompiled contracts: EIP-2935 (history block hashes) at 0x0F
// and EIP-2537 (BLS12-381) at 0x09-0x0E.

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
	common.BytesToAddress([]byte{9}):  &blsG1Add{},
	common.BytesToAddress([]byte{10}): &blsG2Add{},
	common.BytesToAddress([]byte{11}): &blsG1Mul{},
	common.BytesToAddress([]byte{12}): &blsG2Mul{},
	common.BytesToAddress([]byte{13}): &blsPairing{},
	common.BytesToAddress([]byte{14}): &blsMapG1{},
}
