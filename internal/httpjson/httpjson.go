// Package httpjson writes JSON HTTP responses.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// WriteJSON encodes v as the JSON response body with the given status code.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes a JSON error body with the given status code.
func Error(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, map[string]string{"error": msg})
}
