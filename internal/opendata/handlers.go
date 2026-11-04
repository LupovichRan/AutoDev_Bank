package opendata

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// Handlers serves the cached open data as JSON.
type Handlers struct {
	Store     *Store
	Refresher *Refresher
	Log       *slog.Logger
	// RequireStaff guards POST /api/opendata/refresh. It defaults to a
	// pass-through until the auth task injects real middleware; set it
	// before calling Mount.
	RequireStaff func(http.Handler) http.Handler
}

func NewHandlers(store *Store, refresher *Refresher) *Handlers {
	return &Handlers{Store: store, Refresher: refresher, Log: slog.Default(),
		RequireStaff: func(h http.Handler) http.Handler { return h }}
}

// Mount registers the routes on mux.
func (h *Handlers) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/opendata/fx", h.list(KindFXRate, false))
	mux.HandleFunc("GET /api/opendata/rates", h.list(KindPolicyRate, false))
	mux.HandleFunc("GET /api/opendata/atms", h.list(KindATM, true))
	mux.HandleFunc("GET /api/opendata/branches", h.list(KindBranch, true))
	mux.HandleFunc("GET /api/opendata/products", h.list(KindProduct, false))
	mux.HandleFunc("GET /api/opendata/status", h.status)
	guard := h.RequireStaff
	if guard == nil {
		guard = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("POST /api/opendata/refresh", guard(http.HandlerFunc(h.refresh)))
}

func (h *Handlers) list(kind Kind, allowNear bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := parseFilter(r, allowNear)
		if err != nil {
			problem(w, http.StatusBadRequest, "Invalid query", err.Error())
			return
		}
		recs, err := h.Store.Records(r.Context(), kind, f)
		if err != nil {
			h.logger().Error("opendata: list", "kind", kind, "err", err)
			problem(w, http.StatusInternalServerError, "Open data unavailable", "the open data cache could not be read")
			return
		}
		if recs == nil {
			recs = []RecordWithMeta{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": recs, "count": len(recs)})
	}
}

func parseFilter(r *http.Request, allowNear bool) (Filter, error) {
	q := r.URL.Query()
	f := Filter{Bank: strings.TrimSpace(q.Get("bank"))}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return f, errors.New("limit must be an integer between 1 and 1000")
		}
		f.Limit = n
	}
	if s := q.Get("near"); s != "" {
		if !allowNear {
			return f, errors.New("near is only supported for atms and branches")
		}
		lat, lon, ok := strings.Cut(s, ",")
		la, err1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
		lo, err2 := strconv.ParseFloat(strings.TrimSpace(lon), 64)
		if !ok || err1 != nil || err2 != nil || la < -90 || la > 90 || lo < -180 || lo > 180 {
			return f, errors.New("near must be lat,lon in decimal degrees, e.g. near=51.5074,-0.1278")
		}
		f.Near = &LatLon{Lat: la, Lon: lo}
	}
	return f, nil
}

func (h *Handlers) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.Store.Status(r.Context())
	if err != nil {
		h.logger().Error("opendata: status", "err", err)
		problem(w, http.StatusInternalServerError, "Open data unavailable", "the refresh log could not be read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": st})
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	if h.Refresher == nil {
		problem(w, http.StatusServiceUnavailable, "Refresh unavailable", "no refresher is configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": h.Refresher.RefreshAll(r.Context())})
}

func (h *Handlers) logger() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// problem writes an RFC 9457 problem details response. Local to this
// package until the shared httpapi helper exists.
func problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": title, "status": status, "detail": detail,
	})
}
