package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// OBUKConfig describes one bank publishing UK Open Banking Open Data v2.2.
type OBUKConfig struct {
	ID      string          // provider id and key prefix ("natwest")
	Bank    string          // display name stored in Data["bank"]
	BaseURL string          // scheme://host[/prefix]; "/open-banking/v2.2/..." is appended
	Accept  string          // optional Accept header (HSBC requires its vendor type)
	Kinds   []opendata.Kind // subset of atm, branch, product the bank actually serves
}

// OBUK fetches ATMs, branches and personal current accounts from one bank.
type OBUK struct {
	cfg    OBUKConfig
	client *http.Client
}

func NewOBUK(cfg OBUKConfig, client *http.Client) *OBUK {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OBUK{cfg: cfg, client: client}
}

func (p *OBUK) ID() string             { return p.cfg.ID }
func (p *OBUK) Kinds() []opendata.Kind { return slices.Clone(p.cfg.Kinds) }

var obukPaths = map[opendata.Kind]string{
	opendata.KindATM:     "/open-banking/v2.2/atms",
	opendata.KindBranch:  "/open-banking/v2.2/branches",
	opendata.KindProduct: "/open-banking/v2.2/personal-current-accounts",
}

func (p *OBUK) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	if !slices.Contains(p.cfg.Kinds, kind) {
		return opendata.Result{}, unsupported(p.cfg.ID, kind)
	}
	url := p.cfg.BaseURL + obukPaths[kind]
	var hdr http.Header
	if p.cfg.Accept != "" {
		hdr = http.Header{"Accept": {p.cfg.Accept}}
	}
	var doc obukDoc
	if err := getJSON(ctx, p.client, url, hdr, &doc); err != nil {
		return opendata.Result{}, err
	}
	res := opendata.Result{SourceURL: url, License: licenseOrDefault(doc.Meta.License), LicenseURL: obukDefaultLicense}
	seen := map[string]bool{}
	add := func(id string, data map[string]any) {
		key := p.cfg.ID + ":" + string(kind) + ":" + id
		if id == "" || seen[key] {
			return
		}
		seen[key] = true
		res.Records = append(res.Records, opendata.Record{Kind: kind, Key: key, Data: data})
	}
	for _, d := range doc.Data {
		for _, b := range d.Brand {
			switch kind {
			case opendata.KindATM:
				for _, a := range b.ATM {
					add(a.Identification, p.place(siteName(a.Location.Site.Name, a.Location.PostalAddress), a.Location.PostalAddress, bool(a.Access24HoursIndicator)))
				}
			case opendata.KindBranch:
				for _, br := range b.Branch {
					add(br.Identification, p.place(siteName(br.Name, br.PostalAddress), br.PostalAddress, false))
				}
			case opendata.KindProduct:
				for _, pca := range b.PCA {
					add(pca.Identification, p.product(b.BrandName, pca))
				}
			}
		}
	}
	return res, nil
}

func siteName(name string, a obukAddress) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return strings.TrimSpace(a.TownName)
}

func (p *OBUK) place(name string, a obukAddress, access24h bool) map[string]any {
	d := map[string]any{
		"bank":      p.cfg.Bank,
		"name":      name,
		"address":   oneLineAddress(a),
		"postcode":  strings.TrimSpace(a.PostCode),
		"town":      strings.TrimSpace(a.TownName),
		"access24h": access24h,
	}
	if a.GeoLocation != nil {
		lat, okLat := parseNum(string(a.GeoLocation.GeographicCoordinates.Latitude))
		lon, okLon := parseNum(string(a.GeoLocation.GeographicCoordinates.Longitude))
		if okLat && okLon && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && (lat != 0 || lon != 0) {
			d["lat"], d["lon"] = lat, lon
		}
	}
	return d
}

func oneLineAddress(a obukAddress) string {
	var parts []string
	for _, l := range a.AddressLine {
		parts = append(parts, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(l), ";")))
	}
	if len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(strings.TrimSpace(a.BuildingNumber)+" "+strings.TrimSpace(a.StreetName)))
	}
	parts = append(parts, a.TownName, a.PostCode)
	var out []string
	seen := map[string]bool{}
	for _, s := range parts {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func (p *OBUK) product(brand string, pca obukPCA) map[string]any {
	d := map[string]any{
		"bank":        p.cfg.Bank,
		"brand":       strings.TrimSpace(brand),
		"name":        strings.TrimSpace(pca.Name),
		"type":        "personal_current_account",
		"monthly_fee": 0.0,
	}
	if d["brand"] == "" {
		d["brand"] = p.cfg.Bank
	}
	seg := make([]any, 0, len(pca.Segment))
	for _, s := range pca.Segment {
		seg = append(seg, s)
	}
	d["segment"] = seg

	var ear, fee float64
	var haveEAR, haveFee bool
	for _, ms := range pca.PCAMarketingState {
		if u := strings.TrimSpace(ms.CoreProduct.ProductURL); u != "" && d["url"] == nil {
			d["url"] = u
		}
		for _, od := range ms.Overdraft {
			for _, set := range od.OverdraftTierBandSet {
				for _, band := range set.OverdraftTierBand {
					if v, ok := parseNum(band.EAR); ok && (!haveEAR || v > ear) {
						ear, haveEAR = v, true
					}
				}
			}
		}
		for _, ofc := range ms.OtherFeesCharges {
			for _, f := range ofc.FeeChargeDetail {
				if !haveFee && isMonthlyAccountFee(f) {
					if v, ok := parseNum(f.FeeAmount); ok {
						fee, haveFee = v, true
					}
				}
			}
		}
	}
	if haveEAR {
		d["overdraft_ear"] = ear
	}
	if haveFee {
		d["monthly_fee"] = fee
	}
	return d
}

func isMonthlyAccountFee(f obukFee) bool {
	if f.FeeType == "ServiceCAccountFeeMonthly" {
		return true
	}
	return f.FeeType == "ServiceCAccountFee" && strings.EqualFold(f.ApplicationFrequency, "Monthly")
}

// --- wire format (only the fields we read) ---

type obukDoc struct {
	Meta struct {
		License string `json:"License"`
	} `json:"meta"`
	Data []struct {
		Brand oneOrMany[obukBrand] `json:"Brand"`
	} `json:"data"`
}

type obukBrand struct {
	BrandName string    `json:"BrandName"`
	ATM       []obukATM `json:"ATM"`
	Branch    []struct {
		Identification string      `json:"Identification"`
		Name           string      `json:"Name"`
		PostalAddress  obukAddress `json:"PostalAddress"`
	} `json:"Branch"`
	PCA []obukPCA `json:"PCA"`
}

type obukATM struct {
	Identification         string   `json:"Identification"`
	Access24HoursIndicator flexBool `json:"Access24HoursIndicator"`
	Location               struct {
		Site struct {
			Name string `json:"Name"`
		} `json:"Site"`
		PostalAddress obukAddress `json:"PostalAddress"`
	} `json:"Location"`
}

type obukAddress struct {
	AddressLine    []string `json:"AddressLine"`
	BuildingNumber string   `json:"BuildingNumber"`
	StreetName     string   `json:"StreetName"`
	TownName       string   `json:"TownName"`
	PostCode       string   `json:"PostCode"`
	GeoLocation    *struct {
		GeographicCoordinates struct {
			Latitude  flexNum `json:"Latitude"`
			Longitude flexNum `json:"Longitude"`
		} `json:"GeographicCoordinates"`
	} `json:"GeoLocation"`
}

type obukPCA struct {
	Identification    string   `json:"Identification"`
	Name              string   `json:"Name"`
	Segment           []string `json:"Segment"`
	PCAMarketingState oneOrMany[struct {
		CoreProduct struct {
			ProductURL string `json:"ProductURL"`
		} `json:"CoreProduct"`
		Overdraft oneOrMany[struct {
			OverdraftTierBandSet []struct {
				OverdraftTierBand []struct {
					EAR string `json:"EAR"`
				} `json:"OverdraftTierBand"`
			} `json:"OverdraftTierBandSet"`
		}] `json:"Overdraft"`
		OtherFeesCharges oneOrMany[struct {
			FeeChargeDetail []obukFee `json:"FeeChargeDetail"`
		}] `json:"OtherFeesCharges"`
	}] `json:"PCAMarketingState"`
}

type obukFee struct {
	FeeType              string `json:"FeeType"`
	FeeAmount            string `json:"FeeAmount"`
	ApplicationFrequency string `json:"ApplicationFrequency"`
}

// oneOrMany decodes either a JSON object or an array of objects; banks
// disagree (Nationwide sends Brand as an object, others as an array).
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || bytes.Equal(b, []byte("null")):
		*o = nil
		return nil
	case b[0] == '[':
		var s []T
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*o = s
		return nil
	default:
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*o = []T{v}
		return nil
	}
}

// flexNum accepts a JSON number or a string and keeps its decimal text.
type flexNum string

func (f *flexNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexNum(s)
		return nil
	}
	*f = flexNum(b)
	return nil
}

// flexBool accepts true/false or the strings "true"/"false".
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	*f = flexBool(strings.EqualFold(s, "true"))
	return nil
}

const obukDefaultLicense = "https://www.openbanking.org.uk/open-licence"

func licenseOrDefault(s string) string {
	if strings.TrimSpace(s) == "" {
		return obukDefaultLicense
	}
	return s
}
