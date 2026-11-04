package providers

import (
	"net/http"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestOBUK_ParsesATMs(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/open-banking/v2.2/atms": "natwest_atms.json"})
	p := NewOBUK(OBUKConfig{ID: "natwest", Bank: "NatWest", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindATM}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindATM)

	testutil.Len(t, res.Records, 5)
	testutil.Equal(t, "https://www.openbanking.org.uk/open-licence", res.License)
	testutil.Equal(t, srv.URL+"/open-banking/v2.2/atms", res.SourceURL)
	r := res.Records[0]
	testutil.Equal(t, opendata.KindATM, r.Kind)
	testutil.Equal(t, "natwest:atm:r27Oaj", r.Key)
	testutil.Equal(t, any("NatWest"), r.Data["bank"])
	testutil.Equal(t, any(51.4948628), r.Data["lat"])
	testutil.Equal(t, any(-0.1295316), r.Data["lon"])
	testutil.Equal(t, any("SW1P 2AY"), r.Data["postcode"])
	testutil.Equal(t, any(false), r.Data["access24h"])
	testutil.Equal(t, any("Horseferry Road, London, SW1P 2AY"), r.Data["address"])
	testutil.Equal(t, any("London"), r.Data["name"])
}

func TestOBUK_ATMsWithoutCoordinatesOmitLatLon(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/open-banking/v2.2/atms": "lloyds_atms.json"})
	p := NewOBUK(OBUKConfig{ID: "lloyds", Bank: "Lloyds Bank", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindATM}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindATM)
	testutil.Len(t, res.Records, 5)
	_, hasLat := res.Records[0].Data["lat"]
	testutil.False(t, hasLat, "lloyds publishes no GeoLocation")
	testutil.Equal(t, any("OL6 7RU"), res.Records[0].Data["postcode"])
}

func TestOBUK_ParsesBranches(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/open-banking/v2.2/branches": "barclays_branches.json"})
	p := NewOBUK(OBUKConfig{ID: "barclays", Bank: "Barclays", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindATM, opendata.KindBranch}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindBranch)
	testutil.Len(t, res.Records, 3)
	r := res.Records[0]
	testutil.Equal(t, "barclays:branch:20031851", r.Key)
	testutil.Equal(t, any("Barclays Bank"), r.Data["name"])
	testutil.Equal(t, any("LU7 1RX"), r.Data["postcode"])
	testutil.Equal(t, any(51.9150132), r.Data["lat"])
}

func TestOBUK_ParsesProducts(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/open-banking/v2.2/personal-current-accounts": "lloyds_products.json"})
	p := NewOBUK(OBUKConfig{ID: "lloyds", Bank: "Lloyds Bank", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindProduct}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindProduct)
	testutil.Len(t, res.Records, 3)
	m := byKey(res.Records)
	plat := m["lloyds:product:320"]
	testutil.Equal(t, any("Platinum Account"), plat.Data["name"])
	testutil.Equal(t, any("Lloyds Bank"), plat.Data["brand"])
	testutil.Equal(t, any("personal_current_account"), plat.Data["type"])
	testutil.Equal(t, any(29.9), plat.Data["overdraft_ear"])
	testutil.Equal(t, any(22.5), plat.Data["monthly_fee"])
	testutil.Equal(t, any([]any{"Packaged"}), plat.Data["segment"])
	testutil.Equal(t, any("http://www.lloydsbank.com/current-accounts/all-accounts/platinum-account.asp"), plat.Data["url"])
	u19 := m["lloyds:product:301"]
	testutil.Equal(t, any(0.0), u19.Data["monthly_fee"])
	_, hasEAR := u19.Data["overdraft_ear"]
	testutil.False(t, hasEAR)
}

func TestOBUK_ProductsBrandAsObjectAndDuplicates(t *testing.T) {
	// Nationwide returns data[].Brand as an object, not an array.
	srv, _ := fixtureServer(t, map[string]string{
		"/nw/open-banking/v2.2/personal-current-accounts":   "nationwide_products.json",
		"/hsbc/open-banking/v2.2/personal-current-accounts": "hsbc_products.json",
	})
	nw := NewOBUK(OBUKConfig{ID: "nationwide", Bank: "Nationwide", BaseURL: srv.URL + "/nw",
		Kinds: []opendata.Kind{opendata.KindProduct}}, http.DefaultClient)
	testutil.Len(t, fetch(t, nw, opendata.KindProduct).Records, 3)

	// HSBC lists "first direct 1st Account" twice; keys must stay unique.
	hs := NewOBUK(OBUKConfig{ID: "hsbc", Bank: "HSBC UK", BaseURL: srv.URL + "/hsbc",
		Kinds: []opendata.Kind{opendata.KindProduct}}, http.DefaultClient)
	recs := fetch(t, hs, opendata.KindProduct).Records
	testutil.Len(t, recs, 4)
	testutil.Equal(t, any("first direct"), byKey(recs)["hsbc:product:fdFirAcc"].Data["brand"])
	testutil.Equal(t, any(39.9), byKey(recs)["hsbc:product:fdFirAcc"].Data["overdraft_ear"])
}

func TestOBUK_SendsAcceptHeader(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{"/open-banking/v2.2/atms": "hsbc_atms.json"})
	p := NewOBUK(OBUKConfig{ID: "hsbc", Bank: "HSBC UK", BaseURL: srv.URL,
		Accept: "application/prs.openbanking.opendata.v2.2+json",
		Kinds:  []opendata.Kind{opendata.KindATM}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindATM)
	testutil.Len(t, res.Records, 5)
	testutil.Equal(t, "application/prs.openbanking.opendata.v2.2+json", (*seen)[0].Header.Get("Accept"))
}

func TestOBUK_EmptyBodyYieldsNoRecords(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/open-banking/v2.2/atms": "boi_interest.json"})
	p := NewOBUK(OBUKConfig{ID: "natwest", Bank: "NatWest", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindATM}}, http.DefaultClient)
	res := fetch(t, p, opendata.KindATM)
	testutil.Len(t, res.Records, 0)
}

func TestOBUK_HTTPErrorIsError(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{})
	p := NewOBUK(OBUKConfig{ID: "natwest", Bank: "NatWest", BaseURL: srv.URL,
		Kinds: []opendata.Kind{opendata.KindATM}}, http.DefaultClient)
	_, err := p.Fetch(t.Context(), opendata.KindATM)
	testutil.ErrorContains(t, err, "404")
	_, err = p.Fetch(t.Context(), opendata.KindProduct)
	testutil.ErrorContains(t, err, "unsupported kind")
}
