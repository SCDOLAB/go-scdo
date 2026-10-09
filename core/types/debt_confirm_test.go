package types

import "testing"

func TestDebtConfirmationDepthStaysAt120UntilTheFork(t *testing.T) {
	if got := debtConfirmationDepth(5162247, 0, 10000); got != 120 {
		t.Fatalf("unscheduled fork depth = %d, want 120", got)
	}
	if got := debtConfirmationDepth(9499999, 9500000, 10000); got != 120 {
		t.Fatalf("block before the fork depth = %d, want 120", got)
	}
	if got := debtConfirmationDepth(9500000, 9500000, 10000); got != 10000 {
		t.Fatalf("fork block depth = %d, want 10000", got)
	}
	if got := DebtConfirmationDepth(5162247); got != 120 {
		t.Fatalf("production depth = %d, want 120 while the fork height is 0", got)
	}
}
