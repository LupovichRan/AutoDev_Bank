package httpapi

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/audit"
)

// feesJSON is the published fee logic. Rule names and descriptions are
// bilingual ({"he": ..., "en": ...}); the endpoint serves one language.
//
//go:embed fees.json
var feesJSON []byte

// feeLangs are the languages of fees.json; the first is the default.
var feeLangs = []string{"he", "en"}

type bilingual map[string]string

type feeFile struct {
	Currency           string `json:"currency"`
	FXMarkupBP         int    `json:"currency_fx_markup_bp"`
	MonthlyFee         int64  `json:"monthly_fee"`
	IncomeSharePercent int    `json:"income_share_percent"`
	Rules              []struct {
		Name        bilingual `json:"name"`
		Description bilingual `json:"description"`
	} `json:"rules"`
}

type feeRule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type feeSchedule struct {
	Lang               string    `json:"lang"`
	Currency           string    `json:"currency"`
	FXMarkupBP         int       `json:"currency_fx_markup_bp"`
	MonthlyFee         int64     `json:"monthly_fee"`
	IncomeSharePercent int       `json:"income_share_percent"`
	Rules              []feeRule `json:"rules"`
}

// feesByLang is fees.json flattened per language, built once at start-up.
// A rule missing a translation is a build defect, so it panics.
var feesByLang = func() map[string]feeSchedule {
	var f feeFile
	if err := json.Unmarshal(feesJSON, &f); err != nil {
		panic("httpapi: fees.json: " + err.Error())
	}
	out := make(map[string]feeSchedule, len(feeLangs))
	for _, lang := range feeLangs {
		s := feeSchedule{Lang: lang, Currency: f.Currency, FXMarkupBP: f.FXMarkupBP,
			MonthlyFee: f.MonthlyFee, IncomeSharePercent: f.IncomeSharePercent, Rules: make([]feeRule, len(f.Rules))}
		for i, rule := range f.Rules {
			if rule.Name[lang] == "" || rule.Description[lang] == "" {
				panic("httpapi: fees.json: rule " + strconv.Itoa(i) + " has no " + lang + " text")
			}
			s.Rules[i] = feeRule{Name: rule.Name[lang], Description: rule.Description[lang]}
		}
		out[lang] = s
	}
	return out
}()

// GET /api/transparency/fees?lang=he|en (default he)
func (a *api) fees(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = feeLangs[0]
	}
	s, ok := feesByLang[lang]
	if !ok {
		Write(w, r, http.StatusBadRequest, "", "invalid_lang", nil)
		return
	}
	w.Header().Set("Content-Language", lang)
	JSON(w, http.StatusOK, s)
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

// auditEventJSON is the public view of an audit event: no actor, subject
// or payload, the time truncated to the hour and the action reduced to its
// class (the prefix before the first dot), so the feed shows the chain
// growing without becoming a per-person activity timeline.
type auditEventJSON struct {
	Seq        int64     `json:"seq"`
	At         time.Time `json:"at"`
	Action     string    `json:"action"`
	HashPrefix string    `json:"hash_prefix"`
}

// hashPrefixLen is how many hex characters of each event hash are published.
const hashPrefixLen = 16

// actionClass is the part of an audit action before its first dot
// ("transfer.posted" -> "transfer").
func actionClass(action string) string {
	if i := strings.IndexByte(action, '.'); i >= 0 {
		return action[:i]
	}
	return action
}

// GET /api/transparency/audit/recent?limit=20 (max 100).
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
		h := hex.EncodeToString(ev.Hash)
		if len(h) > hashPrefixLen {
			h = h[:hashPrefixLen]
		}
		out[i] = auditEventJSON{Seq: ev.Seq, At: ev.At.UTC().Truncate(time.Hour), Action: actionClass(ev.Action), HashPrefix: h}
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
// stored once. Today is never signed: the answer is the current head,
// {day, head_seq, head_hash, provisional: true}, with no signature.
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
		root audit.Root
		err  error
	)
	if day.Before(today) {
		root, _, err = a.Audit.SignDailyRootIfMissing(r.Context(), day)
	}
	if !day.Before(today) || errors.Is(err, audit.ErrDayNotFinished) {
		head, err := a.Audit.HeadAsOf(r.Context(), day)
		if err != nil {
			WriteErr(w, r, err)
			return
		}
		JSON(w, http.StatusOK, map[string]any{
			"day": head.Day, "head_seq": head.HeadSeq, "head_hash": head.HeadHash, "provisional": true,
		})
		return
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
		"provisional": false,
	})
}
