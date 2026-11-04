package httpapi

import (
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
)

// feesJSON is the published fee logic, served verbatim.
//
//go:embed fees.json
var feesJSON []byte

// GET /api/transparency/fees
func (a *api) fees(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(feesJSON)
}

// GET /api/transparency/audit/verify
func (a *api) auditVerify(w http.ResponseWriter, r *http.Request) {
	rep, err := a.Audit.Verify(r.Context())
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	var firstBad *int64
	if !rep.OK {
		firstBad = &rep.FirstBadSeq
	}
	JSON(w, http.StatusOK, map[string]any{
		"ok": rep.OK, "length": rep.Length, "head_hash": rep.HeadHash, "first_bad_seq": firstBad,
	})
}

type auditEventJSON struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
	ActorID *string   `json:"actor_id"`
}

// GET /api/transparency/audit/recent?limit=20 (max 100). Payloads are not
// published; only what happened, to what, by whom and when.
func (a *api) auditRecent(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			Write(w, r, http.StatusBadRequest, "", "invalid_limit", nil)
			return
		}
		limit = n
	}
	evs, err := a.Audit.Recent(r.Context(), limit)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	out := make([]auditEventJSON, len(evs))
	for i, ev := range evs {
		out[i] = auditEventJSON{Seq: ev.Seq, At: ev.At, Action: ev.Action, Subject: ev.Subject}
		if ev.ActorID != nil {
			s := ev.ActorID.String()
			out[i].ActorID = &s
		}
	}
	JSON(w, http.StatusOK, map[string]any{"events": out})
}

// maxRootAgeDays bounds on-demand signing so the public endpoint cannot be
// used to fill audit_roots with arbitrary historic days.
const maxRootAgeDays = 3660

// GET /api/transparency/audit/root?day=YYYY-MM-DD (default: yesterday, UTC).
// A finished day's stored root is returned as is; a missing one, or one
// signed under a previous key, is signed on demand. Today's root is
// re-signed on every request because the day (and its head) is not over.
func (a *api) auditRoot(w http.ResponseWriter, r *http.Request) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := today.AddDate(0, 0, -1)
	if s := r.URL.Query().Get("day"); s != "" {
		d, err := time.Parse("2006-01-02", s)
		if err != nil || d.After(today) || d.Before(today.AddDate(0, 0, -maxRootAgeDays)) {
			Write(w, r, http.StatusBadRequest, "", "invalid_day", nil)
			return
		}
		day = d
	}
	var root audit.Root
	var head []byte
	err := a.Pool.QueryRow(r.Context(), `select to_char(day, 'YYYY-MM-DD'), head_seq, head_hash, signature
		from control.audit_roots where day = $1::date`, day.Format("2006-01-02")).
		Scan(&root.Day, &root.HeadSeq, &head, &root.Signature)
	root.HeadHash = hex.EncodeToString(head)
	// Re-sign when missing, when the day is not over, or when the stored
	// signature does not verify under the current key (the key was rotated).
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!day.Before(today) || !audit.VerifyRoot(a.Audit.PublicKey(), root))) {
		root, err = a.Audit.SignDailyRoot(r.Context(), day)
	}
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"day": root.Day, "head_seq": root.HeadSeq, "head_hash": root.HeadHash,
		"signature":  base64.StdEncoding.EncodeToString(root.Signature),
		"public_key": base64.StdEncoding.EncodeToString(a.Audit.PublicKey()),
	})
}
