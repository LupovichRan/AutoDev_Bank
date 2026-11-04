package opendata_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// newAPI seeds the cache from the recorded fixtures and mounts the handlers.
func newAPI(t *testing.T) (*http.ServeMux, *opendata.Handlers) {
	t.Helper()
	pool := db.TestPool(t)
	ref := opendata.NewRefresher(pool, providers.FixtureProviders(providers.Fixtures), quiet)
	for _, o := range mustRefresh(t, ref) {
		testutil.True(t, o.OK, o.Provider+" "+string(o.Kind)+": "+o.Error)
	}
	h := opendata.NewHandlers(opendata.NewStore(pool), ref)
	mux := http.NewServeMux()
	h.Mount(mux)
	return mux, h
}

type listResp struct {
	Records []struct {
		Kind      string         `json:"kind"`
		Key       string         `json:"key"`
		Data      map[string]any `json:"data"`
		Provider  string         `json:"provider"`
		FetchedAt time.Time      `json:"fetched_at"`
		SourceURL string         `json:"source_url"`
		License   string         `json:"license"`
	} `json:"records"`
	Count int `json:"count"`
}

func do(t *testing.T, mux http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listResp {
	t.Helper()
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	testutil.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var lr listResp
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &lr))
	testutil.Equal(t, len(lr.Records), lr.Count)
	return lr
}

func TestHandlers_FXReturnsRecordsWithMeta(t *testing.T) {
	mux, _ := newAPI(t)
	lr := decodeList(t, do(t, mux, "GET", "/api/opendata/fx"))
	testutil.True(t, lr.Count > 0)
	providersSeen := map[string]bool{}
	for _, r := range lr.Records {
		providersSeen[r.Provider] = true
		testutil.Equal(t, "fx_rate", r.Kind)
		testutil.False(t, r.FetchedAt.IsZero(), "fetched_at")
		testutil.True(t, strings.HasPrefix(r.SourceURL, "https://"), r.SourceURL)
		testutil.True(t, r.License != "", "license")
		_, isNum := r.Data["rate"].(float64)
		testutil.True(t, isNum, "rate is a JSON number")
	}
	testutil.Equal(t, map[string]bool{"boi": true, "ecb": true, "frankfurter": true}, providersSeen)

	raw := map[string]any{}
	testutil.NoError(t, json.Unmarshal(do(t, mux, "GET", "/api/opendata/fx?bank=ecb").Body.Bytes(), &raw))
	testutil.Equal(t, any(3.0), raw["count"])
	first := raw["records"].([]any)[0].(map[string]any)
	for _, k := range []string{"fetched_at", "source_url", "license", "provider", "key", "kind", "data"} {
		_, ok := first[k]
		testutil.True(t, ok, k)
	}
}

func TestHandlers_ATMsNearSortsByDistance(t *testing.T) {
	mux, _ := newAPI(t)
	lr := decodeList(t, do(t, mux, "GET", "/api/opendata/atms?near=51.5074,-0.1278&limit=20"))
	testutil.Equal(t, 20, lr.Count)
	prev := -1.0
	for _, r := range lr.Records {
		d, ok := r.Data["distance_km"].(float64)
		testutil.True(t, ok, "distance_km on "+r.Key)
		testutil.True(t, d >= prev, "ascending distance")
		prev = d
		testutil.True(t, r.Provider != "lloyds", "lloyds ATMs have no coordinates")
	}
	// HSBC 79 Piccadilly (~1.1 km from Charing Cross) beats NatWest Horseferry Road (~1.5 km).
	testutil.Equal(t, "hsbc:atm:A24A0203", lr.Records[0].Key)
	testutil.Equal(t, any("79 PICCADILLY"), lr.Records[0].Data["name"])
	testutil.True(t, lr.Records[0].Data["distance_km"].(float64) > 1.0 && lr.Records[0].Data["distance_km"].(float64) < 1.2)
	testutil.Equal(t, "natwest:atm:r27Oaj", lr.Records[1].Key)
	testutil.True(t, prev > lr.Records[0].Data["distance_km"].(float64))

	// Without near: all banks, publisher order, no distance.
	all := decodeList(t, do(t, mux, "GET", "/api/opendata/atms"))
	testutil.Equal(t, 25, all.Count)
	_, has := all.Records[0].Data["distance_km"]
	testutil.False(t, has)

	nw := decodeList(t, do(t, mux, "GET", "/api/opendata/atms?bank=NATWEST"))
	testutil.Equal(t, 5, nw.Count)
}

func TestHandlers_OtherKinds(t *testing.T) {
	mux, _ := newAPI(t)
	testutil.Equal(t, 3, decodeList(t, do(t, mux, "GET", "/api/opendata/rates")).Count) // boe, boi, ecb
	testutil.Equal(t, 12, decodeList(t, do(t, mux, "GET", "/api/opendata/branches")).Count)
	prods := decodeList(t, do(t, mux, "GET", "/api/opendata/products?bank=lloyds%20bank"))
	testutil.Equal(t, 3, prods.Count)
	testutil.Equal(t, 2, decodeList(t, do(t, mux, "GET", "/api/opendata/products?limit=2")).Count)
}

func TestHandlers_BadQueryIsProblemJSON(t *testing.T) {
	mux, _ := newAPI(t)
	for _, q := range []string{"near=abc", "near=91,0", "near=1", "near=NaN,NaN", "near=Inf,0", "near=0,-Inf", "limit=0", "limit=x", "limit=5000"} {
		rec := do(t, mux, "GET", "/api/opendata/atms?"+q)
		testutil.Equal(t, http.StatusBadRequest, rec.Code, q)
		testutil.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"), q)
		var p map[string]any
		testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		testutil.Equal(t, any(400.0), p["status"])
		testutil.True(t, p["title"] != nil && p["detail"] != nil, q)
	}
}

func TestHandlers_StatusAndRefresh(t *testing.T) {
	mux, h := newAPI(t)
	var st struct {
		Providers []opendata.ProviderStatus `json:"providers"`
	}
	rec := do(t, mux, "GET", "/api/opendata/status")
	testutil.Equal(t, http.StatusOK, rec.Code)
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	testutil.Equal(t, 19, len(st.Providers)) // one per provider/kind
	for _, p := range st.Providers {
		testutil.True(t, p.OK, p.Provider)
		testutil.True(t, p.FetchedAt != nil && p.Records > 0, p.Provider)
	}

	rec = do(t, mux, "POST", "/api/opendata/refresh")
	testutil.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Outcomes []opendata.RefreshOutcome `json:"outcomes"`
	}
	testutil.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	testutil.Equal(t, 19, len(out.Outcomes))

	testutil.Equal(t, http.StatusMethodNotAllowed, do(t, mux, "GET", "/api/opendata/refresh").Code)

	// RequireStaff is applied when Mount runs.
	h.RequireStaff = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	}
	guarded := http.NewServeMux()
	h.Mount(guarded)
	testutil.Equal(t, http.StatusForbidden, do(t, guarded, "POST", "/api/opendata/refresh").Code)
	testutil.Equal(t, http.StatusOK, do(t, guarded, "GET", "/api/opendata/fx").Code)
}
