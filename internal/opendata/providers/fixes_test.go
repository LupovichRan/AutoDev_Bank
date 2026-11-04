package providers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestEnableBanking_RequiresUIDsAndValidKey(t *testing.T) {
	_, pemKey := testKey(t)
	_, ok := NewEnableBanking("app", pemKey, nil, http.DefaultClient)
	testutil.False(t, ok, "no uids")
	_, ok = NewEnableBanking("app", pemKey, []string{" ", ""}, http.DefaultClient)
	testutil.False(t, ok, "blank uids")
	_, ok = NewEnableBanking("app", []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), []string{"u"}, http.DefaultClient)
	testutil.False(t, ok, "malformed key")
}

func TestEnableBanking_ErrorMasksAccountUID(t *testing.T) {
	_, pemKey := testKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unknown account SECRET-UID-12345", http.StatusNotFound)
	}))
	defer srv.Close()
	p, ok := NewEnableBanking("app", pemKey, []string{"SECRET-UID-12345"}, srv.Client())
	testutil.True(t, ok)
	p.(*EnableBanking).BaseURL = srv.URL
	_, err := p.Fetch(t.Context(), opendata.KindOwnAccount)
	testutil.True(t, err != nil)
	testutil.True(t, !strings.Contains(err.Error(), "SECRET-UID-12345"), err.Error())
	testutil.True(t, strings.Contains(err.Error(), "SECR…"), err.Error())
}

func TestOBUK_NumericCoordinatesAndDefaultLicense(t *testing.T) {
	body := `{"meta":{"License":""},"data":[{"Brand":[{"BrandName":"X","ATM":[{"Identification":"a1",
	"Location":{"PostalAddress":{"TownName":"T","PostCode":"P1","GeoLocation":{"GeographicCoordinates":{"Latitude":51.5,"Longitude":-0.12}}}}}]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	p := NewOBUK(OBUKConfig{ID: "x", Bank: "X", BaseURL: srv.URL, Kinds: []opendata.Kind{opendata.KindATM}}, srv.Client())
	res := fetch(t, p, opendata.KindATM)
	testutil.Equal(t, "https://www.openbanking.org.uk/open-licence", res.License)
	testutil.Len(t, res.Records, 1)
	testutil.Equal(t, any(51.5), res.Records[0].Data["lat"])
	testutil.Equal(t, any(-0.12), res.Records[0].Data["lon"])
}
