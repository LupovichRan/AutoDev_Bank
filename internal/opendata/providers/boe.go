package providers

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LupovichRan/AutoDev_Bank/internal/opendata"
)

// BoE reads Bank Rate (series IUDBEDR) from the Bank of England
// Interactive Statistical Database CSV export and keeps the last
// observation.
type BoE struct {
	base   string
	client *http.Client
}

func NewBoE(baseURL string, client *http.Client) *BoE {
	return &BoE{base: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *BoE) ID() string             { return "boe" }
func (p *BoE) Kinds() []opendata.Kind { return []opendata.Kind{opendata.KindPolicyRate} }

func (p *BoE) Fetch(ctx context.Context, kind opendata.Kind) (opendata.Result, error) {
	if kind != opendata.KindPolicyRate {
		return opendata.Result{}, unsupported(p.ID(), kind)
	}
	url := p.base + "/boeapps/database/_iadb-fromshowcolumns.asp?csv.x=yes&Datefrom=01/Jan/2026&Dateto=now&SeriesCodes=IUDBEDR&CSVF=TN&UsingCodes=Y&VPD=Y&VFD=N"
	body, err := get(ctx, p.client, url, http.Header{"Accept": {"text/csv, */*"}})
	if err != nil {
		return opendata.Result{}, err
	}
	// On intermittent failures the IADB redirects (302) to ErrorPage.asp,
	// which the client follows and which answers 200 with HTML.
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '<' {
		return opendata.Result{}, fmt.Errorf("boe: got an HTML page instead of CSV (IADB error page): %s", snippet(body))
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return opendata.Result{}, fmt.Errorf("boe: csv: %w (body %s)", err, snippet(body))
	}
	if len(rows) == 0 || len(rows[0]) < 2 || strings.TrimSpace(rows[0][0]) != "DATE" || strings.TrimSpace(rows[0][1]) != "IUDBEDR" {
		return opendata.Result{}, fmt.Errorf("boe: unexpected response (body %s)", snippet(body))
	}
	res := opendata.Result{SourceURL: url,
		License: "Bank of England Database (https://www.bankofengland.co.uk/boeapps/database), reuse with attribution to the Bank of England"}
	var last opendata.Record
	var lastT time.Time
	for _, row := range rows[1:] {
		if len(row) < 2 {
			continue
		}
		t, err := time.Parse("02 Jan 2006", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		v, ok := parseNum(row[1])
		if !ok || !t.After(lastT) {
			continue
		}
		lastT = t
		last = policyRecord("boe:policy:IUDBEDR", "Bank of England", "Bank Rate", v, t.Format(time.DateOnly))
	}
	if !lastT.IsZero() {
		res.Records = append(res.Records, last)
	}
	return res, nil
}
