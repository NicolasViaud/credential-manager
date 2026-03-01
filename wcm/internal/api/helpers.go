package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// userIDParam returns the userId path parameter, always percent-decoded.
//
// Chi routes against r.URL.RawPath when present, so the parameter value may be
// percent-encoded (e.g. "alice%40example.com") even if the caller sent it that
// way intentionally. Decoding here normalises the userID regardless of whether
// the client encoded the "@" sign or not.
func userIDParam(r *http.Request) string {
	raw := chi.URLParam(r, "userId")
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
