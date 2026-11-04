package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/LupovichRan/AutoDev_Bank/internal/approvals"
	"github.com/LupovichRan/AutoDev_Bank/internal/auth"
	"github.com/LupovichRan/AutoDev_Bank/internal/ledger"
)

// Write sends an RFC 9457 problem details response. detail is a short,
// stable, machine-matchable string where the web client depends on it
// (e.g. "step_up_required"). ext adds extension members.
func Write(w http.ResponseWriter, r *http.Request, status int, title, detail string, ext map[string]any) {
	if title == "" {
		title = http.StatusText(status)
	}
	body := make(map[string]any, len(ext)+6)
	for k, v := range ext {
		body[k] = v
	}
	body["type"] = "about:blank"
	body["title"] = title
	body["status"] = status
	body["detail"] = detail
	body["instance"] = r.URL.Path
	body["request_id"] = RequestID(r.Context())
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// JSON writes v as a JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errMapping maps typed service errors to a status and stable detail.
// Order matters: more specific errors come first.
var errMapping = []struct {
	err    error
	status int
	detail string
}{
	{auth.ErrStepUpRequired, http.StatusForbidden, "step_up_required"},
	{auth.ErrNoSession, http.StatusUnauthorized, "sign_in_required"},
	{approvals.ErrSelfApproval, http.StatusForbidden, "self_approval"},
	{approvals.ErrNotAuthorized, http.StatusForbidden, "not_authorized"},
	{approvals.ErrNotPending, http.StatusConflict, "not_pending"},
	{approvals.ErrNotBefore, http.StatusConflict, "not_before"},
	{approvals.ErrAlreadyDecided, http.StatusConflict, "already_decided"},
	{approvals.ErrSoDConflict, http.StatusConflict, "separation_of_duties"},
	{approvals.ErrRoleNotAllowed, http.StatusConflict, "separation_of_duties"},
	{approvals.ErrInvalidDestination, http.StatusUnprocessableEntity, "invalid_destination"},
	{approvals.ErrNotFound, http.StatusNotFound, "not_found"},
	{ledger.ErrNotFound, http.StatusNotFound, "not_found"},
	{ledger.ErrIdempotencyConflict, http.StatusConflict, "idempotency_conflict"},
	{ledger.ErrInsufficientFunds, http.StatusUnprocessableEntity, "insufficient_funds"},
	{ledger.ErrCurrencyMismatch, http.StatusUnprocessableEntity, "currency_mismatch"},
	{ledger.ErrPeriodClosed, http.StatusUnprocessableEntity, "period_closed"},
	{ledger.ErrUnbalanced, http.StatusUnprocessableEntity, "invalid_request"},
	{ledger.ErrInvalidRequest, http.StatusUnprocessableEntity, "invalid_request"},
}

// statusFor returns the status and detail WriteErr would use for err.
func statusFor(err error) (int, string) {
	for _, m := range errMapping {
		if errors.Is(err, m.err) {
			return m.status, m.detail
		}
	}
	return http.StatusInternalServerError, "internal_error"
}

// WriteErr maps a service error to a problem response. Unknown errors are
// logged and answered with a generic 500 that leaks nothing.
func WriteErr(w http.ResponseWriter, r *http.Request, err error) {
	status, detail := statusFor(err)
	if status == http.StatusInternalServerError {
		logger(r.Context()).ErrorContext(r.Context(), "httpapi: internal error",
			"path", r.URL.Path, "request_id", RequestID(r.Context()), "err", err)
	}
	Write(w, r, status, "", detail, nil)
}
