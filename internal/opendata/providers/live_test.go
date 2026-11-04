package providers

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLive_AllProvidersReturnRecords hits the real production endpoints.
// Opt-in: LIVE=1 go test ./internal/opendata/... -run Live -v
func TestLive_AllProvidersReturnRecords(t *testing.T) {
	if os.Getenv("LIVE") != "1" {
		t.Skip("set LIVE=1 to hit real open-data endpoints")
	}
	required := map[string]bool{"natwest": true, "lloyds": true, "frankfurter": true, "boi:fx_rate": true, "boe": true}
	for _, p := range NewHTTPProviders(http.DefaultClient) {
		for _, k := range p.Kinds() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			start := time.Now()
			res, err := p.Fetch(ctx, k)
			cancel()
			must := required[p.ID()] || required[p.ID()+":"+string(k)]
			switch {
			case err != nil && must:
				t.Errorf("%s %s: %v", p.ID(), k, err)
			case err != nil:
				t.Logf("FLAKY-OK %s %s: %v", p.ID(), k, err)
			case len(res.Records) == 0 && must:
				t.Errorf("%s %s: 0 records", p.ID(), k)
			default:
				sample := ""
				if len(res.Records) > 0 {
					sample = res.Records[0].Key
				}
				t.Logf("OK %-11s %-11s %5d records in %6dms  e.g. %s", p.ID(), k, len(res.Records), time.Since(start).Milliseconds(), sample)
			}
		}
	}
}
