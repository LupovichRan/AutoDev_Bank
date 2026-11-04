package httpapi

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

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

// verifyCacheTTL is how long a chain verification report is reused.
const verifyCacheTTL = 30 * time.Second

// verifyCache single-flights Verify (a full chain scan) and reuses its
// report for verifyCacheTTL: concurrent callers wait on mu while one runs.
type verifyCache struct {
	mu  sync.Mutex
	at  time.Time
	rep audit.Report
	ok  bool
}

func (a *api) cachedVerify(ctx context.Context) (audit.Report, error) {
	c := &a.verify
	c.mu.Lock()
	defer c.mu.Unlock()
	now := a.now()
	if c.ok && now.Sub(c.at) < verifyCacheTTL {
		return c.rep, nil
	}
	rep, err := a.Audit.Verify(ctx)
	if err != nil {
		return audit.Report{}, err // errors are not cached
	}
	c.rep, c.at, c.ok = rep, now, true
	return rep, nil
}

// GET /api/transparency/audit/verify (cached for verifyCacheTTL)
func (a *api) auditVerify(w http.ResponseWriter, r *http.Request) {
	rep, err := a.cachedVerify(r.Context())
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
//
// A finished day's stored root is returned exactly as stored, never
// re-signed, with verified reporting whether its signature checks out under
// the current public key. A finished day without a stored root is signed and
// stored once. Today's root is signed in memory and not stored
// (provisional: true), because the day and its head are not over.
func (a *api) auditRoot(w http.ResponseWriter, r *http.Request) {
	today := a.now().UTC().Truncate(24 * time.Hour)
	day := today.AddDate(0, 0, -1)
	if s := r.URL.Query().Get("day"); s != "" {
		d, err := time.Parse("2006-01-02", s)
		if err != nil || d.After(today) || d.Before(today.AddDate(0, 0, -maxRootAgeDays)) {
			Write(w, r, http.StatusBadRequest, "", "invalid_day", nil)
			return
		}
		day = d
	}
	var (
		root        audit.Root
		err         error
		provisional bool
	)
	if day.Before(today) {
		root, _, err = a.Audit.SignDailyRootIfMissing(r.Context(), day)
	}
	if !day.Before(today) || errors.Is(err, audit.ErrDayNotFinished) {
		provisional = true
		root, err = a.Audit.ComputeRoot(r.Context(), day)
	}
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	pub := a.Audit.PublicKey()
	JSON(w, http.StatusOK, map[string]any{
		"day": root.Day, "head_seq": root.HeadSeq, "head_hash": root.HeadHash,
		"signature":   base64.StdEncoding.EncodeToString(root.Signature),
		"public_key":  base64.StdEncoding.EncodeToString(pub),
		"verified":    audit.VerifyRoot(pub, root),
		"provisional": provisional,
	})
}
