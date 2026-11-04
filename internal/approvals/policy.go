// Package approvals is the control plane's maker-checker workflow: an
// approval policy decides how many independent approvers a transfer needs,
// and the service either posts it to the ledger immediately or parks it as a
// pending approval that executes once enough eligible principals approve.
//
// Separation of duties is enforced twice, in Go and in the schema (migration
// 0005): statically, no principal may hold both payments.initiator and
// payments.approver (trigger roles_static_sod); dynamically, no principal may
// decide an approval they initiated (trigger decisions_dynamic_sod).
package approvals

import (
	"fmt"

	"github.com/google/uuid"
)

// Tier requires Required approvers for amounts >= Threshold (minor units).
type Tier struct {
	Threshold int64
	Required  int
}

// Policy is the approval policy for outgoing transfers.
type Policy struct {
	Tiers []Tier
	// JointRequiresOtherHolder makes any transfer from an account with more
	// than one holder need at least one approval by another holder.
	JointRequiresOtherHolder bool
}

// DefaultPolicy: >= 1,000.00 needs 1 approver, >= 10,000.00 needs 2, and
// joint accounts need another holder's approval.
func DefaultPolicy() Policy {
	return Policy{
		Tiers:                    []Tier{{Threshold: 100000, Required: 1}, {Threshold: 1000000, Required: 2}},
		JointRequiresOtherHolder: true,
	}
}

// Input is what the policy sees about a proposed transfer.
type Input struct {
	Amount      int64 // minor units, positive
	Currency    string
	InitiatorID uuid.UUID
	HolderIDs   []uuid.UUID // holders of the from-account
	AccountKind string
	// InitiatorKind is the initiator's principal kind (customer, staff, service).
	InitiatorKind string
	// CustomerHolders is how many of HolderIDs are customer principals.
	CustomerHolders int
}

// Decision is the policy outcome: Required == 0 means post immediately.
type Decision struct {
	Required int
	Reason   string
	// RequiresHolder means at least one approval must come from a holder of
	// the from-account other than the initiator (the joint-account rule);
	// other approvals count toward Required but never replace it.
	RequiresHolder bool
}

// Evaluate returns the number of approvals in needs: the maximum of the
// highest matching tier, the staff-initiator rule and the joint-account
// rule (which also sets RequiresHolder).
func (p Policy) Evaluate(in Input) Decision {
	d := Decision{Reason: "below approval threshold"}
	var tier *Tier
	for i := range p.Tiers {
		t := p.Tiers[i]
		if in.Amount >= t.Threshold && t.Required > d.Required {
			tier = &p.Tiers[i]
			d.Required = t.Required
		}
	}
	if tier != nil {
		noun := "approvers"
		if tier.Required == 1 {
			noun = "approver"
		}
		d.Reason = fmt.Sprintf("amount >= %s requires %d %s", formatMinor(tier.Threshold), tier.Required, noun)
	}
	// A staff member debiting a customer's account always needs a checker.
	if in.InitiatorKind == "staff" && in.CustomerHolders > 0 && d.Required < 1 {
		d.Required = 1
		d.Reason = "staff-initiated debit from a customer account requires 1 approver"
	}
	// Joint accounts: another holder must be among the approvers; staff
	// approvals count toward Required but never replace that holder.
	if p.JointRequiresOtherHolder && len(in.HolderIDs) > 1 {
		d.RequiresHolder = true
		if d.Required < 1 {
			d.Required = 1
			d.Reason = "joint account requires another holder"
		} else {
			d.Reason += "; joint account requires another holder among them"
		}
	}
	return d
}

// formatMinor renders minor units with two decimals, e.g. 100000 -> "1000.00".
func formatMinor(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}
