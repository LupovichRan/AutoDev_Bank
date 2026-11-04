package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestFrankfurter_ParsesRates(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{"/v1/latest": "frankfurter_latest.json"})
	p := NewFrankfurter(srv.URL, http.DefaultClient)
	testutil.Equal(t, []opendata.Kind{opendata.KindFXRate}, p.Kinds())
	res := fetch(t, p, opendata.KindFXRate)
	testutil.Equal(t, "EUR", (*seen)[0].URL.Query().Get("base"))
	testutil.Len(t, res.Records, 29)
	m := byKey(res.Records)
	for q, want := range map[string]float64{"GBP": 0.8346, "ILS": 3.5641, "USD": 1.1267} {
		r, ok := m["frankfurter:fx:EUR/"+q]
		testutil.True(t, ok, q)
		testutil.Equal(t, any("EUR"), r.Data["base"])
		testutil.Equal(t, any(q), r.Data["quote"])
		testutil.Equal(t, any(want), r.Data["rate"])
		testutil.Equal(t, any("2026-11-04"), r.Data["date"])
	}
	testutil.Equal(t, srv.URL+"/v1/latest?base=EUR", res.SourceURL)
}

func TestECB_ParsesSDMX(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/service/data/EXR/D.USD+GBP+ILS.EUR.SP00.A": "ecb_exr.json",
		"/service/data/FM/B.U2.EUR.4F.KR.MRR_FR.LEV": "ecb_mrr.json",
	})
	p := NewECB(srv.URL, http.DefaultClient)
	res := fetch(t, p, opendata.KindFXRate)
	testutil.Len(t, res.Records, 3)
	m := byKey(res.Records)
	testutil.Equal(t, any(0.8346), m["ecb:fx:EUR/GBP"].Data["rate"])
	testutil.Equal(t, any(3.5641), m["ecb:fx:EUR/ILS"].Data["rate"])
	testutil.Equal(t, any(1.1267), m["ecb:fx:EUR/USD"].Data["rate"])
	testutil.Equal(t, any("2026-11-04"), m["ecb:fx:EUR/USD"].Data["date"])
	testutil.Equal(t, any("European Central Bank"), m["ecb:fx:EUR/USD"].Data["source"])

	pol := fetch(t, p, opendata.KindPolicyRate)
	testutil.Len(t, pol.Records, 1)
	r := pol.Records[0]
	testutil.Equal(t, "ecb:policy:MRR_FR", r.Key)
	testutil.Equal(t, any(2.65), r.Data["rate"])
	testutil.Equal(t, any("2026-09-16"), r.Data["effective"])
	testutil.Equal(t, any("European Central Bank"), r.Data["bank"])
}

func TestBoI_ParsesRatesAndInterest(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"/PublicApi/GetExchangeRates": "boi_exchange_rates.json",
		"/PublicApi/GetInterest":      "boi_interest.json",
	})
	p := NewBoI(srv.URL, http.DefaultClient)
	fx := fetch(t, p, opendata.KindFXRate)
	m := byKey(fx.Records)
	usd := m["boi:fx:USD/ILS"]
	testutil.Equal(t, any("USD"), usd.Data["base"])
	testutil.Equal(t, any("ILS"), usd.Data["quote"])
	testutil.Equal(t, any(3.157), usd.Data["rate"])
	testutil.Equal(t, any("2026-11-04"), usd.Data["date"])
	testutil.Equal(t, any(0.01992), m["boi:fx:JPY/ILS"].Data["rate"], "JPY is quoted per 100 units")

	pol := fetch(t, p, opendata.KindPolicyRate)
	testutil.Len(t, pol.Records, 1)
	testutil.Equal(t, any("Bank of Israel"), pol.Records[0].Data["bank"])
	testutil.Equal(t, any(3.25), pol.Records[0].Data["rate"])
	testutil.Equal(t, any("2026-11-01"), pol.Records[0].Data["effective"])
}

func TestBoE_ParsesCSVLastObservation(t *testing.T) {
	srv, seen := fixtureServer(t, map[string]string{"/boeapps/database/_iadb-fromshowcolumns.asp": "boe_bank_rate.csv"})
	p := NewBoE(srv.URL, http.DefaultClient)
	res := fetch(t, p, opendata.KindPolicyRate)
	testutil.Equal(t, "IUDBEDR", (*seen)[0].URL.Query().Get("SeriesCodes"))
	testutil.Len(t, res.Records, 1)
	r := res.Records[0]
	testutil.Equal(t, "boe:policy:IUDBEDR", r.Key)
	testutil.Equal(t, any("Bank of England"), r.Data["bank"])
	testutil.Equal(t, any("Bank Rate"), r.Data["name"])
	testutil.Equal(t, any(3.75), r.Data["rate"])
	testutil.Equal(t, any("2026-11-03"), r.Data["effective"])
}

func TestBoE_RejectsNonCSV(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"/boeapps/database/_iadb-fromshowcolumns.asp": "boi_interest.json"})
	_, err := NewBoE(srv.URL, http.DefaultClient).Fetch(t.Context(), opendata.KindPolicyRate)
	testutil.ErrorContains(t, err, "boe: csv")

	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<head><title>Object moved</title></head><body><h1>Object Moved</h1></body>"))
	}))
	defer html.Close()
	_, err = NewBoE(html.URL, http.DefaultClient).Fetch(t.Context(), opendata.KindPolicyRate)
	testutil.ErrorContains(t, err, "HTML page instead of CSV")
}

func TestNewHTTPProviders_RealURLs(t *testing.T) {
	ps := NewHTTPProviders(http.DefaultClient)
	ids := map[string][]opendata.Kind{}
	for _, p := range ps {
		ids[p.ID()] = p.Kinds()
	}
	testutil.Equal(t, []opendata.Kind{opendata.KindATM, opendata.KindBranch}, ids["barclays"])
	testutil.Equal(t, []opendata.Kind{opendata.KindATM, opendata.KindProduct}, ids["nationwide"])
	testutil.Equal(t, []opendata.Kind{opendata.KindATM, opendata.KindBranch, opendata.KindProduct}, ids["natwest"])
	for _, id := range []string{"lloyds", "hsbc", "frankfurter", "ecb", "boi", "boe"} {
		_, ok := ids[id]
		testutil.True(t, ok, id)
	}
}
