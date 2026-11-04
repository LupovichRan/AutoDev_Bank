package httpapi

import (
	"crypto/sha256"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
)

const maxReasonRunes = 500

type decisionJSON struct {
	ApproverID string    `json:"approver_id"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

type approvalJSON struct {
	ID                  string         `json:"id"`
	Kind                string         `json:"kind"`
	Amount              int64          `json:"amount"`
	Currency            string         `json:"currency"`
	Status              string         `json:"status"`
	Initiator           holderJSON     `json:"initiator"`
	RequiredApprovals   int            `json:"required_approvals"`
	Decisions           []decisionJSON `json:"decisions"`
	Subject             subjectJSON    `json:"subject"`
	CreatedAt           time.Time      `json:"created_at"`
	ExpiresAt           time.Time      `json:"expires_at"`
	NotBefore           *time.Time     `json:"not_before"`
	LedgerTransactionID *string        `json:"ledger_transaction_id"`
}

type subjectJSON struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Description   string `json:"description"`
}

func approvalView(ap approvals.Approval, names map[uuid.UUID]string) approvalJSON {
	out := approvalJSON{
		ID: ap.ID.String(), Kind: ap.Kind, Amount: ap.Amount, Currency: ap.Currency, Status: ap.Status,
		Initiator:         holderJSON{PrincipalID: ap.InitiatorID.String(), DisplayName: names[ap.InitiatorID]},
		RequiredApprovals: ap.RequiredApprovals,
		Decisions:         make([]decisionJSON, len(ap.Decisions)),
		Subject: subjectJSON{FromAccountID: ap.Subject.FromAccountID.String(),
			ToAccountID: ap.Subject.ToAccountID.String(), Description: ap.Subject.Description},
		CreatedAt: ap.CreatedAt.UTC(),
		ExpiresAt: ap.ExpiresAt.UTC(),
	}
	if ap.NotBefore != nil {
		nb := ap.NotBefore.UTC()
		out.NotBefore = &nb
	}
	if ap.LedgerTransactionID != nil {
		s := ap.LedgerTransactionID.String()
		out.LedgerTransactionID = &s
	}
	for i, d := range ap.Decisions {
		out.Decisions[i] = decisionJSON{ApproverID: d.ApproverID.String(), Decision: d.Decision, Reason: d.Reason, At: d.At.UTC()}
	}
	return out
}

// views renders approvals with their initiators' display names (one query).
func (a *api) views(r *http.Request, lists ...[]approvals.Approval) ([][]approvalJSON, error) {
	var ids []uuid.UUID
	for _, l := range lists {
		for _, ap := range l {
			ids = append(ids, ap.InitiatorID)
		}
	}
	names, err := a.principalNames(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	out := make([][]approvalJSON, len(lists))
	for i, l := range lists {
		out[i] = make([]approvalJSON, len(l))
		for j, ap := range l {
			out[i][j] = approvalView(ap, names)
		}
	}
	return out, nil
}

// GET /api/approvals: what the caller may decide (inbox) and what they initiated (mine).
func (a *api) listApprovals(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	inbox, err := a.Approvals.Inbox(r.Context(), sess.PrincipalID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	mine, err := a.Approvals.ListForInitiator(r.Context(), sess.PrincipalID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	v, err := a.views(r, inbox, mine)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"inbox": v[0], "mine": v[1]})
}

// POST /api/approvals/{id}/decide {approve, reason}
func (a *api) decideApproval(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Write(w, r, http.StatusNotFound, "", "not_found", nil)
		return
	}
	var req struct {
		Approve *bool  `json:"approve"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Approve == nil {
		Write(w, r, http.StatusUnprocessableEntity, "", "approve_required", nil)
		return
	}
	if !utf8.ValidString(req.Reason) || utf8.RuneCountInString(req.Reason) > maxReasonRunes {
		Write(w, r, http.StatusUnprocessableEntity, "", "invalid_reason", nil)
		return
	}
	// R1: approving moves money, so it needs a fresh passkey step-up bound
	// to this approval, like initiating does. It is consumed before Decide
	// runs (a refused decision has spent it). Rejecting needs none.
	if *req.Approve {
		if err := a.Auth.RequireStepUp(r.Context(), sess, ApprovePayloadHash(id)); err != nil {
			WriteErr(w, r, err)
			return
		}
	}
	ap, err := a.Approvals.Decide(r.Context(), id, sess.PrincipalID, *req.Approve, req.Reason)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	a.writeApproval(w, r, ap)
}

// ApprovePayloadHash is the step-up payload hash that authorises approving
// approval id: sha256("approve:" + id).
func ApprovePayloadHash(id uuid.UUID) []byte {
	h := sha256.Sum256([]byte("approve:" + id.String()))
	return h[:]
}

// POST /api/approvals/{id}/cancel: the initiator withdraws a pending approval.
func (a *api) cancelApproval(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Write(w, r, http.StatusNotFound, "", "not_found", nil)
		return
	}
	var req struct{}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	ap, err := a.Approvals.Cancel(r.Context(), id, sess.PrincipalID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	a.writeApproval(w, r, ap)
}

func (a *api) writeApproval(w http.ResponseWriter, r *http.Request, ap approvals.Approval) {
	v, err := a.views(r, []approvals.Approval{ap})
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"approval": v[0][0]})
}

// GET /api/config: public, non-secret settings the web app needs.
func (a *api) config(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]any{"dev_test_login": a.DevTestLogin, "origin": a.Origin})
}
