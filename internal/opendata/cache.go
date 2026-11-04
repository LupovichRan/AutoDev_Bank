package opendata

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

// Store reads cached snapshots.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Records returns the cached records of one kind across all providers, each
// carrying its snapshot's provenance. With Filter.Near, records without
// coordinates are dropped and the rest sorted by ascending distance, with
// Data["distance_km"] added. Without Near, order is provider id, then the
// publisher's order.
func (s *Store) Records(ctx context.Context, kind Kind, f Filter) ([]RecordWithMeta, error) {
	rows, err := s.pool.Query(ctx, `select provider, fetched_at, source_url, license, payload
		from opendata.snapshots where kind = $1 order by provider`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("opendata: query snapshots: %w", err)
	}
	defer rows.Close()
	bank := strings.TrimSpace(f.Bank)
	var out []RecordWithMeta
	for rows.Next() {
		var (
			provider, src, lic string
			at                 time.Time
			payload            []byte
		)
		if err := rows.Scan(&provider, &at, &src, &lic, &payload); err != nil {
			return nil, err
		}
		var recs []Record
		if err := json.Unmarshal(payload, &recs); err != nil {
			return nil, fmt.Errorf("opendata: decode %s/%s snapshot: %w", provider, kind, err)
		}
		for _, r := range recs {
			if bank != "" && !matchesBank(r, provider, bank) {
				continue
			}
			if f.Near != nil {
				lat, ok1 := r.Data["lat"].(float64)
				lon, ok2 := r.Data["lon"].(float64)
				if !ok1 || !ok2 {
					continue
				}
				r.Data["distance_km"] = math.Round(haversineKM(f.Near.Lat, f.Near.Lon, lat, lon)*1000) / 1000
			}
			out = append(out, RecordWithMeta{Record: r, Provider: provider, FetchedAt: at.UTC(), SourceURL: src, License: lic})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f.Near != nil {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Data["distance_km"].(float64) < out[j].Data["distance_km"].(float64)
		})
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matchesBank(r Record, provider, bank string) bool {
	if strings.EqualFold(provider, bank) {
		return true
	}
	for _, k := range []string{"bank", "source", "brand"} {
		if v, ok := r.Data[k].(string); ok && strings.EqualFold(v, bank) {
			return true
		}
	}
	return false
}

// haversineKM is the great-circle distance on a 6371.0088 km mean-radius sphere.
func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0088
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// ProviderStatus is the latest refresh attempt for one provider and kind.
type ProviderStatus struct {
	Provider   string     `json:"provider"`
	Kind       Kind       `json:"kind"`
	OK         bool       `json:"ok"`
	Error      string     `json:"error"`
	At         time.Time  `json:"at"`                   // when the last attempt ran
	FetchedAt  *time.Time `json:"fetched_at"`           // when the snapshot being served was fetched; null if none
	DurationMS int        `json:"duration_ms"`          // of the last attempt
	Records    int        `json:"records"`              // in the snapshot being served
	SourceURL  string     `json:"source_url,omitempty"` // of the snapshot being served
}

// Status returns the last refresh attempt per (provider, kind), joined with
// the snapshot currently served.
func (s *Store) Status(ctx context.Context) ([]ProviderStatus, error) {
	rows, err := s.pool.Query(ctx, `
		select l.provider, l.kind, l.ok, l.error, l.at, s.fetched_at, l.duration_ms,
		       coalesce(jsonb_array_length(s.payload), 0), coalesce(s.source_url, '')
		from (select distinct on (provider, kind) * from opendata.refresh_log
		      order by provider, kind, id desc) l
		left join opendata.snapshots s on s.provider = l.provider and s.kind = l.kind
		order by l.provider, l.kind`)
	if err != nil {
		return nil, fmt.Errorf("opendata: status: %w", err)
	}
	defer rows.Close()
	out := []ProviderStatus{}
	for rows.Next() {
		var p ProviderStatus
		var kind string
		if err := rows.Scan(&p.Provider, &kind, &p.OK, &p.Error, &p.At, &p.FetchedAt, &p.DurationMS, &p.Records, &p.SourceURL); err != nil {
			return nil, err
		}
		p.Kind = Kind(kind)
		p.At = p.At.UTC()
		if p.FetchedAt != nil {
			t := p.FetchedAt.UTC()
			p.FetchedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
