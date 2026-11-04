package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// fixtureServer serves recorded responses: path -> testdata file name.
// Every request is recorded so tests can assert headers.
func fixtureServer(t *testing.T, routes map[string]string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(context.Background()))
		name, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := Fixtures.ReadFile("testdata/" + name)
		if err != nil {
			t.Errorf("fixture %s: %v", name, err)
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func fetch(t *testing.T, p opendata.Provider, kind opendata.Kind) opendata.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := p.Fetch(ctx, kind)
	if err != nil {
		t.Fatalf("%s %s: %v", p.ID(), kind, err)
	}
	return res
}

func byKey(recs []opendata.Record) map[string]opendata.Record {
	m := make(map[string]opendata.Record, len(recs))
	for _, r := range recs {
		m[r.Key] = r
	}
	return m
}
