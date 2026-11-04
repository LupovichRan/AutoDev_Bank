package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

// maxBodyBytes caps every JSON request body.
const maxBodyBytes = 64 << 10

// decodeJSON decodes exactly one JSON value from r's body into v, rejecting
// a Content-Type other than application/json (415), unknown fields,
// trailing data and bodies over 64 KiB. On failure it writes the problem
// response and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		Write(w, r, http.StatusUnsupportedMediaType, "", "unsupported_media_type", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes) // also capped globally by limitBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data after JSON value")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Write(w, r, http.StatusRequestEntityTooLarge, "", "body_too_large", nil)
			return false
		}
		Write(w, r, http.StatusBadRequest, "", "invalid_json", nil)
		return false
	}
	return true
}
