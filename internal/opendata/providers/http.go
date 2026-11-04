// Package providers holds the concrete upstream publishers for package
// opendata: UK Open Banking Open Data (OBUK) banks, Frankfurter, the ECB,
// the Bank of Israel, the Bank of England and (credentialed, opt-in)
// Enable Banking. Every provider takes an *http.Client and a base URL so
// tests can point it at an httptest.Server.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

const (
	userAgent = "AutoDevBank-opendata/0.1 (+https://github.com/LupovichRan/AutoDev_Bank)"
	maxBody   = 32 << 20 // the largest OBUK feed (branches) is ~1.4 MB today
)

// NewHTTPProviders returns every zero-credential provider wired to its real
// production base URL.
func NewHTTPProviders(client *http.Client) []opendata.Provider {
	atm, br, prod := opendata.KindATM, opendata.KindBranch, opendata.KindProduct
	return []opendata.Provider{
		NewOBUK(OBUKConfig{ID: "natwest", Bank: "NatWest", BaseURL: "https://openapi.natwest.com", Kinds: []opendata.Kind{atm, br, prod}}, client),
		NewOBUK(OBUKConfig{ID: "lloyds", Bank: "Lloyds Bank", BaseURL: "https://api.lloydsbank.com", Kinds: []opendata.Kind{atm, br, prod}}, client),
		// Barclays answers 406 to the HSBC-style Accept header and 404 for products.
		NewOBUK(OBUKConfig{ID: "barclays", Bank: "Barclays", BaseURL: "https://atlas.api.barclays", Kinds: []opendata.Kind{atm, br}}, client),
		NewOBUK(OBUKConfig{ID: "hsbc", Bank: "HSBC UK", BaseURL: "https://api.hsbc.com", Accept: "application/prs.openbanking.opendata.v2.2+json", Kinds: []opendata.Kind{atm, br, prod}}, client),
		// Nationwide's branches endpoint returns 403 AccessDenied.
		NewOBUK(OBUKConfig{ID: "nationwide", Bank: "Nationwide", BaseURL: "https://openapi.nationwide.co.uk", Kinds: []opendata.Kind{atm, prod}}, client),
		NewFrankfurter("https://api.frankfurter.dev", client),
		NewECB("https://data-api.ecb.europa.eu", client),
		NewBoI("https://boi.org.il", client),
		NewBoE("https://www.bankofengland.co.uk", client),
	}
}

// get performs a GET and returns the body of a 2xx response.
func get(ctx context.Context, client *http.Client, url string, header http.Header) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, snippet(body))
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("GET %s: body exceeds %d bytes", url, maxBody)
	}
	return body, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, header http.Header, v any) error {
	body, err := get(ctx, client, url, header)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode %s: %w (body %s)", url, err, snippet(body))
	}
	return nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func unsupported(id string, k opendata.Kind) error {
	return fmt.Errorf("%s: unsupported kind %q", id, k)
}

// parseNum parses a decimal string such as "051.49486280"; ok is false for
// empty or malformed input.
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// round keeps rates free of binary noise introduced by division (e.g. 1.992/100).
func round(f float64, digits int) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', digits, 64), 64)
	return v
}
