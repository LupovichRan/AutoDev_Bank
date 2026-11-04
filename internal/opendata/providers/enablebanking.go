package providers

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// EnableBanking is the credentialed, opt-in provider for the bank's own
// accounts held at other institutions (PSD2 AIS via Enable Banking). It
// authenticates with an RS256 JWT signed by the application's private key
// and reads balances for pre-authorised account uids. Its own_account
// records are private and are not served by the public open-data API.
type EnableBanking struct {
	BaseURL     string
	appID       string
	key         *rsa.PrivateKey
	accountUIDs []string
	client      *http.Client
	now         func() time.Time
}

// NewEnableBanking returns (nil, false) when appID or the key is missing or
// the PEM is not an RSA private key (PKCS#8 or PKCS#1) or no account uid is
// configured.
func NewEnableBanking(appID string, privateKeyPEM []byte, accountUIDs []string, client *http.Client) (opendata.Provider, bool) {
	if strings.TrimSpace(appID) == "" || len(privateKeyPEM) == 0 {
		return nil, false
	}
	var uids []string
	for _, u := range accountUIDs {
		if u = strings.TrimSpace(u); u != "" {
			uids = append(uids, u)
		}
	}
	if len(uids) == 0 {
		return nil, false
	}
	key, err := parseRSAKey(privateKeyPEM)
	if err != nil {
		// The error carries only the parser's reason, never key material.
		slog.Warn("enablebanking: private key rejected; provider disabled", "err", err)
		return nil, false
	}
	return &EnableBanking{BaseURL: "https://api.enablebanking.com", appID: strings.TrimSpace(appID),
		key: key, accountUIDs: uids, client: client, now: time.Now}, true
}

// EnableBankingFromEnv reads ENABLE_BANKING_APP_ID, the PEM from
// ENABLE_BANKING_PRIVATE_KEY (inline) or ENABLE_BANKING_PRIVATE_KEY_FILE,
// and the comma-separated ENABLE_BANKING_ACCOUNT_UIDS.
func EnableBankingFromEnv(client *http.Client) (opendata.Provider, bool) {
	pemKey := []byte(os.Getenv("ENABLE_BANKING_PRIVATE_KEY"))
	if f := os.Getenv("ENABLE_BANKING_PRIVATE_KEY_FILE"); len(pemKey) == 0 && f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, false
		}
		pemKey = b
	}
	return NewEnableBanking(os.Getenv("ENABLE_BANKING_APP_ID"), pemKey,
		strings.Split(os.Getenv("ENABLE_BANKING_ACCOUNT_UIDS"), ","), client)
}

// maskUID keeps the first 4 characters so logs and /status stay correlatable
// without exposing the full account identifier.
func maskUID(uid string) string {
	r := []rune(uid)
	if len(r) <= 4 {
		return "…"
	}
	return string(r[:4]) + "…"
}

func parseRSAKey(b []byte) (*rsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("enablebanking: no PEM block")
	}
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("enablebanking: key is not RSA")
	}
	return x509.ParsePKCS1PrivateKey(blk.Bytes)
}

func (p *EnableBanking) ID() string             { return "enablebanking" }
func (p *EnableBanking) Kinds() []opendata.Kind { return []opendata.Kind{opendata.KindOwnAccount} }

// jwt builds the application token: header {alg RS256, typ JWT, kid appID},
// claims {iss enablebanking.com, aud api.enablebanking.com, iat, exp=iat+3600}.
func (p *EnableBanking) jwt(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	hdr, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": p.appID})
	if err != nil {
		return "", err
	}
	iat := now.Unix()
	claims, err := json.Marshal(map[string]any{"iss": "enablebanking.com", "aud": "api.enablebanking.com", "iat": iat, "exp": iat + 3600})
	if err != nil {
		return "", err
	}
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (p *EnableBanking) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	if kind != opendata.KindOwnAccount {
		return opendata.Result{}, unsupported(p.ID(), kind)
	}
	tok, err := p.jwt(p.now())
	if err != nil {
		return opendata.Result{}, err
	}
	base := strings.TrimRight(p.BaseURL, "/")
	res := opendata.Result{SourceURL: base + "/accounts/{uid}/balances", License: "Enable Banking AIS (private, own accounts)"}
	hdr := http.Header{"Authorization": {"Bearer " + tok}}
	for _, uid := range p.accountUIDs {
		var doc struct {
			Balances []struct {
				BalanceAmount struct {
					Currency string `json:"currency"`
					Amount   string `json:"amount"`
				} `json:"balance_amount"`
				BalanceType   string `json:"balance_type"`
				ReferenceDate string `json:"reference_date"`
			} `json:"balances"`
		}
		if err := getJSON(ctx, p.client, base+"/accounts/"+url.PathEscape(uid)+"/balances", hdr, &doc); err != nil {
			return opendata.Result{}, fmt.Errorf("enablebanking: account %s: %s", maskUID(uid), strings.NewReplacer(
				url.PathEscape(uid), maskUID(uid), uid, maskUID(uid)).Replace(err.Error()))
		}
		for _, b := range doc.Balances {
			res.Records = append(res.Records, opendata.Record{
				Kind: opendata.KindOwnAccount,
				Key:  "enablebanking:own_account:" + uid + ":" + b.BalanceType,
				Data: map[string]any{
					"provider": "enablebanking", "account_uid": uid,
					"balance_amount": b.BalanceAmount.Amount, "currency": b.BalanceAmount.Currency,
					"balance_type": b.BalanceType, "reference_date": b.ReferenceDate,
				},
			})
		}
	}
	return res, nil
}
