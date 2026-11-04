package providers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// ECB reads the ECB Data Portal SDMX-JSON API: EXR daily reference rates
// for USD, GBP and ILS, and the main refinancing operations fixed rate.
type ECB struct {
	base   string
	client *http.Client
}

func NewECB(baseURL string, client *http.Client) *ECB {
	return &ECB{base: strings.TrimRight(baseURL, "/"), client: client}
}

// Short factual attribution; the publisher's terms at ecbLicenseURL govern.
const (
	ecbLicense    = "ECB Data Portal; source: European Central Bank"
	ecbLicenseURL = "https://www.ecb.europa.eu/services/using-our-site/disclaimer/html/index.en.html"
)

func (p *ECB) ID() string { return "ecb" }
func (p *ECB) Kinds() []opendata.Kind {
	return []opendata.Kind{opendata.KindFXRate, opendata.KindPolicyRate}
}

func (p *ECB) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	switch kind {
	case opendata.KindFXRate:
		url := p.base + "/service/data/EXR/D.USD+GBP+ILS.EUR.SP00.A?lastNObservations=1&format=jsondata"
		obs, err := p.latest(ctx, url, "CURRENCY")
		if err != nil {
			return opendata.Result{}, err
		}
		res := opendata.Result{SourceURL: url, License: ecbLicense, LicenseURL: ecbLicenseURL}
		for _, o := range obs {
			res.Records = append(res.Records, fxRecord("ecb", "EUR", o.dim, o.value, o.period, "European Central Bank"))
		}
		return res, nil
	case opendata.KindPolicyRate:
		url := p.base + "/service/data/FM/B.U2.EUR.4F.KR.MRR_FR.LEV?lastNObservations=1&format=jsondata"
		obs, err := p.latest(ctx, url, "PROVIDER_FM_ID")
		if err != nil {
			return opendata.Result{}, err
		}
		res := opendata.Result{SourceURL: url, License: ecbLicense, LicenseURL: ecbLicenseURL}
		for _, o := range obs {
			res.Records = append(res.Records, policyRecord("ecb:policy:"+o.dim, "European Central Bank",
				"Main refinancing operations - fixed rate", o.value, o.period))
		}
		return res, nil
	}
	return opendata.Result{}, unsupported(p.ID(), kind)
}

type sdmxObs struct {
	dim    string // value of the requested series dimension (e.g. "GBP")
	period string // TIME_PERIOD of the latest observation
	value  float64
}

// latest returns, per series, the latest observation, labelled with the
// value of series dimension dimID. Results are sorted by that label.
func (p *ECB) latest(ctx context.Context, url, dimID string) ([]sdmxObs, error) {
	var doc struct {
		DataSets []struct {
			Series map[string]struct {
				Observations map[string][]*float64 `json:"observations"`
			} `json:"series"`
		} `json:"dataSets"`
		Structure struct {
			Dimensions struct {
				Series      []sdmxDim `json:"series"`
				Observation []sdmxDim `json:"observation"`
			} `json:"dimensions"`
		} `json:"structure"`
	}
	if err := getJSON(ctx, p.client, url, http.Header{"Accept": {"application/json"}}, &doc); err != nil {
		return nil, err
	}
	if len(doc.DataSets) == 0 {
		return nil, nil
	}
	dimPos := -1
	for i, d := range doc.Structure.Dimensions.Series {
		if d.ID == dimID {
			dimPos = i
		}
	}
	var periods []sdmxValue
	for _, d := range doc.Structure.Dimensions.Observation {
		if d.ID == "TIME_PERIOD" {
			periods = d.Values
		}
	}
	if dimPos < 0 {
		return nil, fmt.Errorf("ecb: dimension %s missing in %s", dimID, url)
	}
	dimVals := doc.Structure.Dimensions.Series[dimPos].Values
	var out []sdmxObs
	for key, s := range doc.DataSets[0].Series {
		idx := strings.Split(key, ":")
		if dimPos >= len(idx) {
			continue
		}
		vi, err := strconv.Atoi(idx[dimPos])
		if err != nil || vi < 0 || vi >= len(dimVals) {
			continue
		}
		best := -1
		var val float64
		for ok, v := range s.Observations {
			oi, err := strconv.Atoi(ok)
			if err != nil || len(v) == 0 || v[0] == nil || oi < 0 || oi >= len(periods) {
				continue
			}
			if oi > best {
				best, val = oi, *v[0]
			}
		}
		if best < 0 {
			continue
		}
		out = append(out, sdmxObs{dim: dimVals[vi].ID, period: periods[best].ID, value: val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dim < out[j].dim })
	return out, nil
}

type sdmxDim struct {
	ID     string      `json:"id"`
	Values []sdmxValue `json:"values"`
}

type sdmxValue struct {
	ID string `json:"id"`
}
