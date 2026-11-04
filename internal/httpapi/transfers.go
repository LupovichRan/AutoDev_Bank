package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
)

// transferBody is the POST /api/transfers request. Its five fields are
// exactly the ones the step-up signs (web/src/lib/payload.ts).
type transferBody struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Description   string `json:"description"`
}

// maxSafeAmount is JavaScript's Number.MAX_SAFE_INTEGER: above it the web
// client cannot represent (or hash) the amount exactly.
const maxSafeAmount = 1<<53 - 1

const maxDescriptionRunes = 140

var (
	currencyRE       = regexp.MustCompile(`^[A-Z]{3}$`)
	idempotencyKeyRE = regexp.MustCompile(`^[\x21-\x7e]{8,128}$`) // printable ASCII, no spaces
)

// canonicalTransferJSON is the compact JSON object of the signed fields in
// lexicographic key order, strings encoded by encoding/json without HTML
// escaping. For descriptions accepted by validateTransferBody it is byte
// for byte what the web client's JSON.stringify produces.
func canonicalTransferJSON(in transferBody) []byte {
	var buf bytes.Buffer
	str := func(s string) {
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(s)           // a string cannot fail to encode
		buf.Truncate(buf.Len() - 1) // drop Encode's trailing newline
	}
	buf.WriteString(`{"amount":`)
	buf.WriteString(jsonInt(in.Amount))
	buf.WriteString(`,"currency":`)
	str(in.Currency)
	buf.WriteString(`,"description":`)
	str(in.Description)
	buf.WriteString(`,"from_account_id":`)
	str(in.FromAccountID)
	buf.WriteString(`,"to_account_id":`)
	str(in.ToAccountID)
	buf.WriteString(`}`)
	return buf.Bytes()
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// transferPayloadHash is sha256(canonicalTransferJSON(in)): the payload hash
// the step-up ceremony was bound to (dynamic linking).
func transferPayloadHash(in transferBody) []byte {
	sum := sha256.Sum256(canonicalTransferJSON(in))
	return sum[:]
}

// validateTransferBody checks the request and parses the account ids. On
// failure detail is the stable problem detail, else "".
func validateTransferBody(in transferBody) (from, to uuid.UUID, detail string) {
	var err1, err2 error
	from, err1 = uuid.Parse(in.FromAccountID)
	to, err2 = uuid.Parse(in.ToAccountID)
	switch {
	case in.Amount <= 0 || in.Amount > maxSafeAmount:
		return from, to, "invalid_amount"
	case !currencyRE.MatchString(in.Currency):
		return from, to, "invalid_currency"
	case !validDescription(in.Description):
		return from, to, "invalid_description"
	case err1 != nil || err2 != nil || from == to:
		return from, to, "invalid_account"
	}
	return from, to, ""
}

// validDescription allows up to 140 characters of valid UTF-8 without
// control characters. U+2028 and U+2029 are refused too: encoding/json
// escapes them and JSON.stringify does not, so the payload hashes would
// differ and the step-up could never match.
func validDescription(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxDescriptionRunes {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// POST /api/transfers
func (a *api) createTransfer(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	key := r.Header.Get("Idempotency-Key")
	if !idempotencyKeyRE.MatchString(key) {
		Write(w, r, http.StatusBadRequest, "", "idempotency_key_required",
			map[string]any{"hint": "send an Idempotency-Key header of 8-128 printable characters"})
		return
	}
	var body transferBody
	if !decodeJSON(w, r, &body) {
		return
	}
	from, to, detail := validateTransferBody(body)
	if detail != "" {
		Write(w, r, http.StatusUnprocessableEntity, "", detail, nil)
		return
	}

	// The step-up is consumed before the transfer runs: a transfer that
	// then fails has spent it and the user confirms again (safe direction).
	if err := a.Auth.RequireStepUp(r.Context(), sess, transferPayloadHash(body)); err != nil {
		WriteErr(w, r, err)
		return
	}

	in := approvals.TransferInput{
		// Keys are namespaced per principal so one customer's key can never
		// collide with (or probe) another's, nor with internal seed:* keys.
		IdempotencyKey: "api:" + sess.PrincipalID.String() + ":" + key,
		InitiatorID:    sess.PrincipalID,
		FromAccountID:  from,
		ToAccountID:    to,
		Amount:         body.Amount,
		Currency:       body.Currency,
		Description:    body.Description,
	}
	out, err := a.Approvals.SubmitTransfer(r.Context(), in)
	a.auditSubmitted(r, sess, key, body, out, err)
	switch {
	case err != nil:
		WriteErr(w, r, err)
	case out.Posted != nil:
		// Only the caller's own side of the posting: the payee's entry (and
		// its balance_after) is none of the payer's business.
		held, err := a.heldAccounts(r.Context(), sess.PrincipalID, out.Posted.Entries)
		if err != nil {
			WriteErr(w, r, err)
			return
		}
		JSON(w, http.StatusCreated, map[string]any{"transaction": transactionView(*out.Posted, held)})
	default:
		names, err := a.principalNames(r.Context(), []uuid.UUID{out.Approval.InitiatorID})
		if err != nil {
			WriteErr(w, r, err)
			return
		}
		JSON(w, http.StatusAccepted, map[string]any{"approval": approvalView(*out.Approval, names)})
	}
}

// auditSubmitted appends transfer.submitted in its own small transaction
// after the transfer call, whatever its outcome. The money movement (if
// any) is already committed and audited by the approvals service, so a
// failure here is logged, not returned.
func (a *api) auditSubmitted(r *http.Request, sess auth.Session, key string, body transferBody, out approvals.Outcome, err error) {
	outcome := ""
	switch {
	case err != nil:
		_, detail := statusFor(err)
		outcome = "refused:" + detail
	case out.Posted != nil:
		outcome = "posted"
	default:
		outcome = "approval_required"
	}
	payload := map[string]any{
		"idempotency_key": key, "amount": body.Amount, "currency": body.Currency,
		"from_account_id": body.FromAccountID, "to_account_id": body.ToAccountID, "outcome": outcome,
	}
	subject := "transfer"
	switch {
	case out.Posted != nil:
		subject = "ledger_transaction:" + out.Posted.ID.String()
		payload["transaction_id"] = out.Posted.ID.String()
	case out.Approval != nil:
		subject = "approval:" + out.Approval.ID.String()
		payload["approval_id"] = out.Approval.ID.String()
	}
	// Detach from the request so a client disconnect cannot drop the event.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	actor := sess.PrincipalID
	aerr := db.WithTx(ctx, a.Pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := a.Audit.Append(ctx, tx, audit.Event{ActorID: &actor, Action: "transfer.submitted", Subject: subject, Payload: payload})
		return err
	})
	if aerr != nil {
		logger(ctx).ErrorContext(ctx, "httpapi: audit transfer.submitted", "request_id", RequestID(ctx), "err", aerr)
	}
}

type transactionJSON struct {
	ID             string      `json:"id"`
	IdempotencyKey string      `json:"idempotency_key"`
	Description    string      `json:"description"`
	Currency       string      `json:"currency"`
	Status         string      `json:"status"`
	EffectiveDate  string      `json:"effective_date"`
	CreatedAt      time.Time   `json:"created_at"`
	Entries        []entryLite `json:"entries"`
}

type entryLite struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	BalanceAfter int64  `json:"balance_after"`
}

// transactionView renders t with only the entries on accounts in held (the
// caller's own accounts).
func transactionView(t ledger.Transaction, held map[uuid.UUID]bool) transactionJSON {
	out := transactionJSON{
		ID: t.ID.String(), IdempotencyKey: t.IdempotencyKey, Description: t.Description,
		Currency: t.Currency, Status: t.Status, EffectiveDate: t.EffectiveDate.Format("2006-01-02"),
		CreatedAt: t.CreatedAt.UTC(), Entries: []entryLite{},
	}
	for _, e := range t.Entries {
		if out.Currency == "" {
			out.Currency = e.Currency
		}
		if !held[e.AccountID] {
			continue
		}
		out.Entries = append(out.Entries, entryLite{ID: e.ID.String(), AccountID: e.AccountID.String(),
			Amount: e.Amount, Currency: e.Currency, BalanceAfter: e.BalanceAfter})
	}
	return out
}

// heldAccounts reports which of the entries' accounts principalID holds.
func (a *api) heldAccounts(ctx context.Context, principalID uuid.UUID, entries []ledger.Entry) (map[uuid.UUID]bool, error) {
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.AccountID)
	}
	rows, err := a.Pool.Query(ctx, `select account_id from control.account_holders
		where principal_id = $1 and account_id = any($2)`, principalID, ids)
	if err != nil {
		return nil, err
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(held))
	for _, id := range held {
		out[id] = true
	}
	return out, nil
}

// principalNames returns display names for ids in one query.
func (a *api) principalNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := a.Pool.Query(ctx, `select id, display_name from control.principals where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
