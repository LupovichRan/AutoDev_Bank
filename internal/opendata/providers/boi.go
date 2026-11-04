package providers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// BoI reads the Bank of Israel public API: representative exchange rates
// (ILS per foreign unit) and the Bank of Israel interest rate.
type BoI struct {
	base   string
	client *http.Client
}

func NewBoI(baseURL string, client *http.Client) *BoI {
	return &BoI{base: strings.TrimRight(baseURL, "/"), client: client}
}

// Short factual attribution; the publisher's terms at boiLicenseURL govern.
const (
	boiLicense    = "Bank of Israel public data; source: Bank of Israel"
	boiLicenseURL = "https://www.boi.org.il/en/"
)

func (p *BoI) ID() string { return "boi" }
func (p *BoI) Kinds() []opendata.Kind {
	return []opendata.Kind{opendata.KindFXRate, opendata.KindPolicyRate}
}

func (p *BoI) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	switch kind {
	case opendata.KindFXRate:
		url := p.base + "/PublicApi/GetExchangeRates"
		var doc struct {
			ExchangeRates []struct {
				Key                 string  `json:"key"`
				CurrentExchangeRate float64 `json:"currentExchangeRate"`
				Unit                float64 `json:"unit"`
				LastUpdate          string  `json:"lastUpdate"`
			} `json:"exchangeRates"`
		}
		if err := getJSON(ctx, p.client, url, nil, &doc); err != nil {
			return opendata.Result{}, err
		}
		res := opendata.Result{SourceURL: url, License: boiLicense, LicenseURL: boiLicenseURL}
		for _, r := range doc.ExchangeRates {
			if r.Key == "" || r.CurrentExchangeRate <= 0 {
				continue
			}
			unit := r.Unit
			if unit <= 0 {
				unit = 1
			}
			res.Records = append(res.Records, fxRecord("boi", strings.ToUpper(r.Key), "ILS",
				round(r.CurrentExchangeRate/unit, 10), isoDate(r.LastUpdate), "Bank of Israel"))
		}
		return res, nil
	case opendata.KindPolicyRate:
		url := p.base + "/PublicApi/GetInterest"
		var doc struct {
			CurrentInterest   *float64 `json:"currentInterest"`
			LastPublishedDate string   `json:"lastPublishedDate"`
			NextInterestDate  string   `json:"nextInterestDate"`
		}
		if err := getJSON(ctx, p.client, url, nil, &doc); err != nil {
			return opendata.Result{}, err
		}
		res := opendata.Result{SourceURL: url, License: boiLicense, LicenseURL: boiLicenseURL}
		if doc.CurrentInterest != nil {
			rec := policyRecord("boi:policy:interest", "Bank of Israel", "Bank of Israel interest rate",
				*doc.CurrentInterest, isoDate(doc.LastPublishedDate))
			if d := isoDate(doc.NextInterestDate); d != "" {
				rec.Data["next_decision"] = d
			}
			res.Records = append(res.Records, rec)
		}
		return res, nil
	}
	return opendata.Result{}, unsupported(p.ID(), kind)
}

// isoDate turns an RFC 3339 timestamp (any precision) into YYYY-MM-DD.
func isoDate(s string) string {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.DateOnly)
	}
	if len(s) >= 10 {
		if _, err := time.Parse(time.DateOnly, s[:10]); err == nil {
			return s[:10]
		}
	}
	return ""
}
