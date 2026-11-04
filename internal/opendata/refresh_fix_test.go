package opendata_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func mustRefresh(t *testing.T, r *opendata.Refresher) []opendata.RefreshOutcome {
	t.Helper()
	out, err := r.RefreshAll(t.Context())
	testutil.NoError(t, err)
	return out
}

// A 200 response whose body is not JSON must fail the attempt and keep the
// previous snapshot.
func TestRefresh_GarbageBodyKeepsPrevious(t *testing.T) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)
	testutil.True(t, mustRefresh(t, r)[0].OK)
	before, _ := readSnap(t, pool, "frankfurter", opendata.KindFXRate)

	s.mode.Store("garbage")
	out := mustRefresh(t, r)
	testutil.False(t, out[0].OK)
	testutil.True(t, out[0].Error != "")
	after, ok := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.True(t, ok)
	testutil.Equal(t, before, after, "snapshot must be unchanged")
	l, n := lastLog(t, pool, "frankfurter")
	testutil.Equal(t, 2, n)
	testutil.False(t, l.ok)
}

// A second run while one is in flight is rejected, also over HTTP (409).
func TestRefresh_OverlapRejected(t *testing.T) {
	pool := db.TestPool(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	good, err := providers.Fixtures.ReadFile("testdata/frankfurter_latest.json")
	testutil.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write(good)
	}))
	t.Cleanup(srv.Close)
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(srv.URL, srv.Client())}, quiet)

	done := make(chan error, 1)
	go func() { _, err := r.RefreshAll(context.Background()); done <- err }()
	<-entered

	_, err = r.RefreshAll(t.Context())
	if err != opendata.ErrRefreshInProgress {
		t.Fatalf("want ErrRefreshInProgress, got %v", err)
	}
	h := opendata.NewHandlers(opendata.NewStore(pool), r)
	mux := http.NewServeMux()
	h.Mount(mux)
	rec := do(t, mux, "POST", "/api/opendata/refresh")
	testutil.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	testutil.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	close(release)
	testutil.NoError(t, <-done)
	// The lock is released afterwards.
	_, err = r.RefreshAll(t.Context())
	testutil.NoError(t, err)
}

// A cancelled run (shutdown) leaves no ok=false rows behind.
func TestRefresh_CancelledWritesNoFailureRow(t *testing.T) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := r.RefreshAll(ctx)
	testutil.NoError(t, err)
	testutil.False(t, out[0].OK)
	var n int
	testutil.NoError(t, pool.QueryRow(context.Background(), `select count(*) from opendata.refresh_log where provider='frankfurter'`).Scan(&n))
	testutil.Equal(t, 0, n)
}
