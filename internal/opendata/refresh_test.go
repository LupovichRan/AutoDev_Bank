package opendata_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/db"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/opendata/providers"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// switchServer serves the Frankfurter fixture, then whatever mode says.
type switchServer struct {
	mode atomic.Value // "good" | "empty" | "500"
	srv  *httptest.Server
}

func newSwitchServer(t *testing.T) *switchServer {
	s := &switchServer{}
	s.mode.Store("good")
	good, err := providers.Fixtures.ReadFile("testdata/frankfurter_latest.json")
	testutil.NoError(t, err)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch s.mode.Load().(string) {
		case "empty":
			_, _ = w.Write([]byte(`{}`))
		case "garbage":
			_, _ = w.Write([]byte("<html>not json</html>"))
		case "500":
			http.Error(w, "upstream down", http.StatusInternalServerError)
		default:
			_, _ = w.Write(good)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

type snap struct {
	fetchedAt time.Time
	sourceURL string
	license   string
	count     int
}

func readSnap(t *testing.T, pool *pgxpool.Pool, provider string, kind opendata.Kind) (snap, bool) {
	t.Helper()
	var s snap
	err := pool.QueryRow(context.Background(), `select fetched_at, source_url, license, jsonb_array_length(payload)
		from opendata.snapshots where provider=$1 and kind=$2`, provider, string(kind)).Scan(&s.fetchedAt, &s.sourceURL, &s.license, &s.count)
	if err != nil {
		return snap{}, false
	}
	return s, true
}

type logRow struct {
	ok      bool
	errText string
	ms      int
}

func lastLog(t *testing.T, pool *pgxpool.Pool, provider string) (logRow, int) {
	t.Helper()
	var l logRow
	var n int
	testutil.NoError(t, pool.QueryRow(context.Background(), `select count(*) from opendata.refresh_log where provider=$1`, provider).Scan(&n))
	testutil.NoError(t, pool.QueryRow(context.Background(), `select ok, error, duration_ms from opendata.refresh_log
		where provider=$1 order by id desc limit 1`, provider).Scan(&l.ok, &l.errText, &l.ms))
	return l, n
}

func TestRefresh_StoresSnapshotAndLog(t *testing.T) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)

	out := mustRefresh(t, r)
	testutil.Len(t, out, 1)
	testutil.True(t, out[0].OK, out[0].Error)
	testutil.Equal(t, "frankfurter", out[0].Provider)
	testutil.Equal(t, opendata.KindFXRate, out[0].Kind)
	testutil.Equal(t, 29, out[0].Records)

	sn, ok := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.True(t, ok, "snapshot stored")
	testutil.Equal(t, 29, sn.count)
	testutil.Equal(t, s.srv.URL+"/v1/latest?base=EUR", sn.sourceURL)
	testutil.True(t, sn.license != "")
	l, n := lastLog(t, pool, "frankfurter")
	testutil.Equal(t, 1, n)
	testutil.True(t, l.ok)
	testutil.Equal(t, "", l.errText)
}

func testKeepsPrevious(t *testing.T, mode, wantErr string) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)
	testutil.True(t, mustRefresh(t, r)[0].OK)
	before, _ := readSnap(t, pool, "frankfurter", opendata.KindFXRate)

	s.mode.Store(mode)
	out := mustRefresh(t, r)
	testutil.False(t, out[0].OK)
	testutil.ErrorContains(t, errString(out[0].Error), wantErr)

	after, ok := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.True(t, ok)
	testutil.Equal(t, before, after, "snapshot must be unchanged")
	l, n := lastLog(t, pool, "frankfurter")
	testutil.Equal(t, 2, n)
	testutil.False(t, l.ok)
	testutil.ErrorContains(t, errString(l.errText), wantErr)

	// Store keeps serving the previous snapshot.
	recs, err := opendata.NewStore(pool).Records(t.Context(), opendata.KindFXRate, opendata.Filter{})
	testutil.NoError(t, err)
	testutil.Len(t, recs, 29)
}

func TestRefresh_KeepsPreviousSnapshotOnBadBody(t *testing.T) {
	testKeepsPrevious(t, "empty", "no records")
}

func TestRefresh_KeepsPreviousSnapshotOnHTTP500(t *testing.T) {
	testKeepsPrevious(t, "500", "HTTP 500")
}

func TestRefresh_FixtureFallbackOnlyWithoutLiveSnapshot(t *testing.T) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	s.mode.Store("500")
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)
	r.Fallback = providers.FixtureProviders(providers.Fixtures)
	r.UseFixtures = true

	out := mustRefresh(t, r)
	testutil.False(t, out[0].OK, "live fetch failed")
	testutil.True(t, out[0].Fallback, "fixture stored")
	sn, ok := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.True(t, ok)
	testutil.Equal(t, "https://api.frankfurter.dev/v1/latest?base=EUR (fixture)", sn.sourceURL)
	l, _ := lastLog(t, pool, "frankfurter")
	testutil.ErrorContains(t, errString(l.errText), "fixture fallback")

	// A live success replaces the fixture...
	s.mode.Store("good")
	testutil.True(t, mustRefresh(t, r)[0].OK)
	live, _ := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.Equal(t, s.srv.URL+"/v1/latest?base=EUR", live.sourceURL)

	// ...and a later failure never downgrades live data back to the fixture.
	s.mode.Store("500")
	out = mustRefresh(t, r)
	testutil.False(t, out[0].Fallback)
	kept, _ := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.Equal(t, live, kept)
}

func TestRefresh_FixturesDisabledByDefault(t *testing.T) {
	pool := db.TestPool(t)
	s := newSwitchServer(t)
	s.mode.Store("500")
	r := opendata.NewRefresher(pool, []opendata.Provider{providers.NewFrankfurter(s.srv.URL, s.srv.Client())}, quiet)
	r.Fallback = providers.FixtureProviders(providers.Fixtures)
	out := mustRefresh(t, r)
	testutil.False(t, out[0].Fallback)
	_, ok := readSnap(t, pool, "frankfurter", opendata.KindFXRate)
	testutil.False(t, ok)
}

type errString string

func (e errString) Error() string { return string(e) }
