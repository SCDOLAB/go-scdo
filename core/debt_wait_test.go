/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package core

import (
	"math/big"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/crypto"
	"github.com/stretchr/testify/assert"
)

func TestDebtPoolKeepsUnsyncedDebt(t *testing.T) {
	common.LocalShardNumber = 1
	defer func() { common.LocalShardNumber = common.UndefinedShardNumber }()

	from := *crypto.MustGenerateShardAddress(2)
	to := *crypto.MustGenerateShardAddress(1)
	data := types.DebtData{
		From:    from,
		Account: to,
		Amount:  big.NewInt(1),
		Price:   big.NewInt(1),
	}
	debt := &types.Debt{Data: data, Hash: data.Hash()}
	bc := NewTestBlockchain()

	notSynced := errors.NewStackedError(types.ErrHeaderNotReady, "source shard header is not synced yet")
	pool := NewDebtPool(bc, types.NewTestVerifier(false, false, notSynced))
	assert.Nil(t, pool.toConfirmedDebts.add(debt))
	err := pool.DoMulCheckingDebtHandler(debt)
	assert.NotNil(t, err)
	assert.True(t, pool.toConfirmedDebts.has(debt.Hash))

	noPeers := errors.NewStackedError(types.ErrHeaderNotReady, "No peers found")
	poolPeers := NewDebtPool(bc, types.NewTestVerifier(false, false, noPeers))
	assert.Nil(t, poolPeers.toConfirmedDebts.add(debt))
	err = poolPeers.DoMulCheckingDebtHandler(debt)
	assert.NotNil(t, err)
	assert.True(t, poolPeers.toConfirmedDebts.has(debt.Hash))

	invalid := NewDebtPool(bc, types.NewTestVerifier(false, false, errors.New("bad debt")))
	assert.Nil(t, invalid.toConfirmedDebts.add(debt))
	err = invalid.DoMulCheckingDebtHandler(debt)
	assert.NotNil(t, err)
	assert.False(t, invalid.toConfirmedDebts.has(debt.Hash))

	// The field failure: the ODR server has not stored the source tx. The debt
	// stays queued. It is not marked valid and it is not dropped.
	peerMiss := errors.New("failed to validate debt via verifier ===> failed to get tx 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> failed to handle ODR request on server side ===> failed to get tx by hash 0xd9cc2ad7dff881a357a8fc46e36c2bc6d21e62bd80a9e12f8144a15db8c5816b ===> leveldb: not found")
	poolMiss := NewDebtPool(bc, types.NewTestVerifier(false, false, peerMiss))
	assert.Nil(t, poolMiss.toConfirmedDebts.add(debt))
	err = poolMiss.DoMulCheckingDebtHandler(debt)
	assert.NotNil(t, err)
	assert.True(t, poolMiss.toConfirmedDebts.has(debt.Hash))

	timedOut := errors.New("failed to get tx 0x72ba09d19faa24ee0bf804b5fa74151fc9814fb3235e0a0eef41a7be776d5fd0 ===> wait for msg reqid=70714341 timeout")
	poolTimeout := NewDebtPool(bc, types.NewTestVerifier(false, false, timedOut))
	assert.Nil(t, poolTimeout.toConfirmedDebts.add(debt))
	err = poolTimeout.DoMulCheckingDebtHandler(debt)
	assert.NotNil(t, err)
	assert.True(t, poolTimeout.toConfirmedDebts.has(debt.Hash))
}
