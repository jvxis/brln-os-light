package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func normalizePaymentRequest(value string) string {
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), "")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeErrorCode(w, status, "", message)
}

func writeErrorCode(w http.ResponseWriter, status int, code string, message string) {
	payload := map[string]string{"error": message}
	if strings.TrimSpace(code) != "" {
		payload["code"] = code
	}
	writeJSON(w, status, payload)
}
