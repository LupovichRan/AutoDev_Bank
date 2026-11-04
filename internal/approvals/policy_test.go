package approvals

import (
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/ids"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/google/uuid"
)

func policyInput(amount int64, holders int) Input {
	in := Input{Amount: amount, Currency: "ILS", InitiatorID: ids.New(), AccountKind: "asset"}
	in.HolderIDs = []uuid.UUID{in.InitiatorID}
	for i := 1; i < holders; i++ {
		in.HolderIDs = append(in.HolderIDs, ids.New())
	}
	return in
}

func TestPolicy_BelowTierNoApproval(t *testing.T) {
	d := DefaultPolicy().Evaluate(policyInput(99999, 1))
	testutil.Equal(t, 0, d.Required)
	testutil.Equal(t, "below approval threshold", d.Reason)
}

func TestPolicy_Tier1OneApprover(t *testing.T) {
	for _, amt := range []int64{100000, 999999} {
		d := DefaultPolicy().Evaluate(policyInput(amt, 1))
		testutil.Equal(t, 1, d.Required)
		testutil.Equal(t, "amount >= 1000.00 requires 1 approver", d.Reason)
	}
}

func TestPolicy_Tier2TwoApprovers(t *testing.T) {
	d := DefaultPolicy().Evaluate(policyInput(1000000, 1))
	testutil.Equal(t, 2, d.Required)
	testutil.Equal(t, "amount >= 10000.00 requires 2 approvers", d.Reason)
}

func TestPolicy_JointAccountRequiresOtherHolder(t *testing.T) {
	d := DefaultPolicy().Evaluate(policyInput(500, 2))
	testutil.Equal(t, 1, d.Required)
	testutil.Equal(t, "joint account requires another holder", d.Reason)

	// The joint rule can be switched off.
	p := DefaultPolicy()
	p.JointRequiresOtherHolder = false
	testutil.Equal(t, 0, p.Evaluate(policyInput(500, 2)).Required)

	// A larger tier requirement wins over the joint rule.
	testutil.Equal(t, 2, DefaultPolicy().Evaluate(policyInput(1000000, 2)).Required)
}

func TestPolicy_JointRuleRequiresHolder(t *testing.T) {
	// The joint rule flags the decision whatever the tier says.
	for _, amt := range []int64{500, 200000, 1000000} {
		d := DefaultPolicy().Evaluate(policyInput(amt, 2))
		testutil.True(t, d.RequiresHolder, "joint account requires a co-holder approval")
	}
	testutil.False(t, DefaultPolicy().Evaluate(policyInput(200000, 1)).RequiresHolder, "single holder")
	p := DefaultPolicy()
	p.JointRequiresOtherHolder = false
	testutil.False(t, p.Evaluate(policyInput(500, 2)).RequiresHolder, "rule switched off")
}

func TestPolicy_StaffInitiatedCustomerDebitNeedsApproval(t *testing.T) {
	in := policyInput(500, 1)
	in.HolderIDs = []uuid.UUID{ids.New()} // the staff initiator is not a holder
	in.InitiatorKind = "staff"
	in.CustomerHolders = 1
	d := DefaultPolicy().Evaluate(in)
	testutil.Equal(t, 1, d.Required)
	testutil.Equal(t, "staff-initiated debit from a customer account requires 1 approver", d.Reason)

	// A higher tier still wins.
	in.Amount = 1000000
	testutil.Equal(t, 2, DefaultPolicy().Evaluate(in).Required)

	// Staff moving money out of an account with no customer holders is tier-driven only.
	in.Amount, in.CustomerHolders = 500, 0
	testutil.Equal(t, 0, DefaultPolicy().Evaluate(in).Required)
}
