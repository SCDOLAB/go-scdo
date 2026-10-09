package types

import "github.com/scdoproject/go-scdo/common"

// DebtConfirmationDepth is how many source-shard blocks must sit above the
// source transaction before a debt is confirmed for a block at blockHeight.
// Before the irreversible fork this is ConfirmedBlockNumber (120). At and
// after the fork it is DebtIrreversibleConfirmations. A fork height of zero
// leaves every block on the 120 rule.
func DebtConfirmationDepth(blockHeight uint64) uint64 {
	return debtConfirmationDepth(blockHeight, common.DebtIrreversibleForkHeight, common.DebtIrreversibleConfirmations)
}

func debtConfirmationDepth(blockHeight, fork, deep uint64) uint64 {
	if fork > 0 && blockHeight >= fork {
		if deep == 0 {
			return common.ConfirmedBlockNumber
		}
		return deep
	}
	return common.ConfirmedBlockNumber
}
