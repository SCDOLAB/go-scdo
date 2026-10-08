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
}
