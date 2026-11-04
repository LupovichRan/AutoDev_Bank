// Package opendata fetches zero-credential public banking data (UK Open
// Banking Open Data v2.2 ATMs, branches and products; central-bank FX and
// policy rates), caches the last good snapshot per provider and kind in
// PostgreSQL, and serves it over HTTP with provenance (fetched_at,
// source_url, license) attached to every record.
package opendata

import (
	"context"
	"time"
)

// Kind is the type of a record. Every Kind has a documented Data shape.
type Kind string

const (
	// KindFXRate Data:
	//   base   string  ISO 4217 code of the base currency ("EUR")
	//   quote  string  ISO 4217 code of the quote currency ("GBP")
	//   rate   float64 units of quote per 1 unit of base
	//   date   string  YYYY-MM-DD the rate refers to
	//   source string  human name of the publisher ("European Central Bank")
	KindFXRate Kind = "fx_rate"
	// KindPolicyRate Data:
	//   bank      string  central bank ("Bank of England")
	//   name      string  rate name ("Bank Rate")
	//   rate      float64 percent per annum (3.75 means 3.75%)
	//   effective string  YYYY-MM-DD of the observation the rate is taken from
	KindPolicyRate Kind = "policy_rate"
	// KindATM and KindBranch Data:
	//   bank      string   display name of the bank ("NatWest")
	//   name      string   site or branch name (falls back to town)
	//   lat, lon  float64  WGS84; absent when the publisher omits coordinates
	//   address   string   one-line postal address
	//   postcode  string
	//   town      string
	//   access24h bool     Access24HoursIndicator (false when not published)
	KindATM    Kind = "atm"
	KindBranch Kind = "branch"
	// KindProduct Data (personal current accounts):
	//   bank          string
	//   brand         string   brand within the bank group ("first direct")
	//   name          string   product name
	//   type          string   "personal_current_account"
	//   segment       []any    OBIE segments ("Basic", "Packaged", ...)
	//   overdraft_ear float64  highest published arranged-overdraft EAR, percent; absent if none
	//   monthly_fee   float64  monthly account fee in GBP; 0 when none is published
	//   url           string   product page; absent when not published
	KindProduct Kind = "product"
	// KindOwnAccount Data (Enable Banking, credentialed, never served publicly):
	//   provider, account_uid, balance_amount (decimal string), currency,
	//   balance_type, reference_date
	KindOwnAccount Kind = "own_account"
)

// Record is one normalised item. Key is stable across refreshes, e.g.
// "natwest:atm:r27Oaj" or "frankfurter:fx:EUR/GBP".
type Record struct {
	Kind Kind           `json:"kind"`
	Key  string         `json:"key"`
	Data map[string]any `json:"data"`
}

// Result is what one Fetch returns: the records plus their provenance.
type Result struct {
	Records   []Record
	SourceURL string
	License   string // short factual attribution
	// LicenseURL is the publisher's own terms page; License is our summary
	// of it, LicenseURL is what counts.
	LicenseURL string
}

// Provider is one upstream publisher. Fetch is called once per Kind the
// provider declares and must honour ctx cancellation.
type Provider interface {
	ID() string
	Kinds() []Kind
	Fetch(ctx context.Context, kind Kind) (Result, error)
}

// LatLon is a WGS84 point.
type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Filter narrows Store.Records.
type Filter struct {
	Bank  string  // case-insensitive match on Data["bank"], Data["source"] or the provider id
	Near  *LatLon // when set, records with coordinates sorted by distance (Data["distance_km"]); records without coordinates are dropped
	Limit int     // default 100, max 1000
}

// RecordWithMeta is a cached record plus the provenance of its snapshot.
type RecordWithMeta struct {
	Record
	Provider   string    `json:"provider"`
	FetchedAt  time.Time `json:"fetched_at"`
	SourceURL  string    `json:"source_url"`
	License    string    `json:"license"`
	LicenseURL string    `json:"license_url"`
}
