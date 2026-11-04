package providers

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// Frankfurter serves ECB reference rates (open source, no key).
type Frankfurter struct {
	base   string
	client *http.Client
}

func NewFrankfurter(baseURL string, client *http.Client) *Frankfurter {
	return &Frankfurter{base: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *Frankfurter) ID() string             { return "frankfurter" }
func (p *Frankfurter) Kinds() []opendata.Kind { return []opendata.Kind{opendata.KindFXRate} }

func (p *Frankfurter) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	if kind != opendata.KindFXRate {
		return opendata.Result{}, unsupported(p.ID(), kind)
	}
	url := p.base + "/v1/latest?base=EUR"
	var doc struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := getJSON(ctx, p.client, url, nil, &doc); err != nil {
		return opendata.Result{}, err
	}
	res := opendata.Result{SourceURL: url,
		License: "Frankfurter (open source, https://frankfurter.dev); rates are ECB euro foreign exchange reference rates, reuse with attribution to the European Central Bank"}
	quotes := make([]string, 0, len(doc.Rates))
	for q := range doc.Rates {
		quotes = append(quotes, q)
	}
	sort.Strings(quotes)
	if doc.Base == "" {
		return res, nil
	}
	for _, q := range quotes {
		res.Records = append(res.Records, fxRecord("frankfurter", doc.Base, q, doc.Rates[q], doc.Date, "Frankfurter (ECB reference rates)"))
	}
	return res, nil
}

func fxRecord(provider, base, quote string, rate float64, date, source string) opendata.Record {
	return opendata.Record{
		Kind: opendata.KindFXRate,
		Key:  provider + ":fx:" + base + "/" + quote,
		Data: map[string]any{"base": base, "quote": quote, "rate": rate, "date": date, "source": source},
	}
}

func policyRecord(key, bank, name string, rate float64, effective string) opendata.Record {
	return opendata.Record{
		Kind: opendata.KindPolicyRate,
		Key:  key,
		Data: map[string]any{"bank": bank, "name": name, "rate": rate, "effective": effective},
	}
}
