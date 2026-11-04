package providers

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	testutil.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(k)
	testutil.NoError(t, err)
	return k, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func decodeSeg(t *testing.T, s string, v any) {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	testutil.NoError(t, err)
	testutil.NoError(t, json.Unmarshal(b, v))
}

func TestEnableBanking_DisabledWithoutConfig(t *testing.T) {
	_, pemKey := testKey(t)
	_, ok := NewEnableBanking("", pemKey, []string{"u"}, http.DefaultClient)
	testutil.False(t, ok)
	_, ok = NewEnableBanking("app", nil, []string{"u"}, http.DefaultClient)
	testutil.False(t, ok)
	_, ok = NewEnableBanking("app", []byte("not a pem"), []string{"u"}, http.DefaultClient)
	testutil.False(t, ok)
	_, ok = EnableBankingFromEnv(http.DefaultClient)
	testutil.False(t, ok, "env unset in tests")
}

func TestEnableBanking_JWTIsValidRS256(t *testing.T) {
	key, pemKey := testKey(t)
	p, ok := NewEnableBanking("app-123", pemKey, []string{"u"}, http.DefaultClient)
	testutil.True(t, ok)
	eb := p.(*EnableBanking)
	now := time.Unix(1_793_000_000, 0)
	tok, err := eb.jwt(now)
	testutil.NoError(t, err)

	parts := strings.Split(tok, ".")
	testutil.Len(t, parts, 3)
	var hdr map[string]any
	decodeSeg(t, parts[0], &hdr)
	testutil.Equal(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": "app-123"}, hdr)
	var claims map[string]any
	decodeSeg(t, parts[1], &claims)
	testutil.Equal(t, any("enablebanking.com"), claims["iss"])
	testutil.Equal(t, any("api.enablebanking.com"), claims["aud"])
	testutil.Equal(t, any(float64(now.Unix())), claims["iat"])
	testutil.Equal(t, any(float64(now.Unix()+3600)), claims["exp"])

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	testutil.NoError(t, err)
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	testutil.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig))
}

func TestEnableBanking_PKCS1KeyAccepted(t *testing.T) {
	key, _ := testKey(t)
	pem1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	_, ok := NewEnableBanking("app", pem1, []string{"u"}, http.DefaultClient)
	testutil.True(t, ok)
}

func TestEnableBanking_FetchesBalances(t *testing.T) {
	key, pemKey := testKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok, found := strings.CutPrefix(auth, "Bearer ")
		parts := strings.Split(tok, ".")
		if !found || len(parts) != 3 {
			http.Error(w, "no token", 401)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
			http.Error(w, "bad sig", 401)
			return
		}
		if r.URL.Path != "/accounts/uid-1/balances" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"balances":[{"name":"Booked","balance_amount":{"currency":"EUR","amount":"1234.56"},"balance_type":"CLBD","reference_date":"2026-11-03"}]}`))
	}))
	defer srv.Close()

	p, ok := NewEnableBanking("app", pemKey, []string{"uid-1"}, srv.Client())
	testutil.True(t, ok)
	p.(*EnableBanking).BaseURL = srv.URL
	testutil.Equal(t, []opendata.Kind{opendata.KindOwnAccount}, p.Kinds())
	res := fetch(t, p, opendata.KindOwnAccount)
	testutil.Len(t, res.Records, 1)
	r := res.Records[0]
	testutil.Equal(t, "enablebanking:own_account:uid-1:CLBD", r.Key)
	testutil.Equal(t, map[string]any{
		"provider": "enablebanking", "account_uid": "uid-1", "balance_amount": "1234.56",
		"currency": "EUR", "balance_type": "CLBD", "reference_date": "2026-11-03",
	}, r.Data)
}
