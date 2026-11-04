package providers

import (
	"embed"
	"io/fs"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// Fixtures holds one trimmed real production response per provider and
// kind, recorded with curl on 2026-11-04. Files live under "testdata/".
//
//go:embed testdata
var Fixtures embed.FS

// FixtureRoutes maps the real upstream host+path to its recorded file.
var FixtureRoutes = map[string]string{
	"openapi.natwest.com/open-banking/v2.2/atms":                           "natwest_atms.json",
	"openapi.natwest.com/open-banking/v2.2/branches":                       "natwest_branches.json",
	"openapi.natwest.com/open-banking/v2.2/personal-current-accounts":      "natwest_products.json",
	"api.lloydsbank.com/open-banking/v2.2/atms":                            "lloyds_atms.json",
	"api.lloydsbank.com/open-banking/v2.2/branches":                        "lloyds_branches.json",
	"api.lloydsbank.com/open-banking/v2.2/personal-current-accounts":       "lloyds_products.json",
	"atlas.api.barclays/open-banking/v2.2/atms":                            "barclays_atms.json",
	"atlas.api.barclays/open-banking/v2.2/branches":                        "barclays_branches.json",
	"api.hsbc.com/open-banking/v2.2/atms":                                  "hsbc_atms.json",
	"api.hsbc.com/open-banking/v2.2/branches":                              "hsbc_branches.json",
	"api.hsbc.com/open-banking/v2.2/personal-current-accounts":             "hsbc_products.json",
	"openapi.nationwide.co.uk/open-banking/v2.2/atms":                      "nationwide_atms.json",
	"openapi.nationwide.co.uk/open-banking/v2.2/personal-current-accounts": "nationwide_products.json",
	"api.frankfurter.dev/v1/latest":                                        "frankfurter_latest.json",
	"data-api.ecb.europa.eu/service/data/EXR/D.USD+GBP+ILS.EUR.SP00.A":     "ecb_exr.json",
	"data-api.ecb.europa.eu/service/data/FM/B.U2.EUR.4F.KR.MRR_FR.LEV":     "ecb_mrr.json",
	"boi.org.il/PublicApi/GetExchangeRates":                                "boi_exchange_rates.json",
	"boi.org.il/PublicApi/GetInterest":                                     "boi_interest.json",
	"www.bankofengland.co.uk/boeapps/database/_iadb-fromshowcolumns.asp":   "boe_bank_rate.csv",
}

// FixtureProviders returns the same providers as NewHTTPProviders, with the
// real URLs, but answered from recorded files in fsys (files at
// "testdata/<name>", i.e. pass Fixtures). They back the refresher's
// BANK_OPENDATA_FIXTURES fallback and offline demos.
func FixtureProviders(fsys fs.FS) []opendata.Provider {
	routes := make(map[string]string, len(FixtureRoutes))
	for k, v := range FixtureRoutes {
		routes[k] = "testdata/" + v
	}
	return NewHTTPProviders(opendata.FixtureClient(fsys, routes))
}
