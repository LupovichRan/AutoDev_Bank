package providers

import (
	"strings"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

// Every provider/kind wired in NewHTTPProviders has a recorded fixture that
// parses to at least one record.
func TestFixtureProviders_EveryKindHasRecords(t *testing.T) {
	ps := FixtureProviders(Fixtures)
	testutil.Equal(t, len(NewHTTPProviders(nil)), len(ps))
	for _, p := range ps {
		for _, k := range p.Kinds() {
			res := fetch(t, p, k)
			testutil.True(t, len(res.Records) > 0, p.ID()+" "+string(k))
			testutil.True(t, strings.HasPrefix(res.SourceURL, "https://"), res.SourceURL)
			testutil.True(t, res.License != "", p.ID()+" license")
		}
	}
}
