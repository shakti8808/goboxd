// Package handlers contains the HTTP handlers for GoboxD's endpoints.
//
// Each handler is described in the architecture spec §6 (Components and
// Interfaces) under its component name (Health_Handler, Info_Handler,
// Run_Handler). Phase 1 ships Health_Handler and Info_Handler in full and
// Run_Handler as a 503 not_implemented stub; the full Run_Handler lands in
// Phase 2.
package handlers

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes a JSON body with the given status code.
//
// The Content-Type header is set to application/json. Encoding errors are
// silently dropped; the client connection is the only observer and any
// emission failure surfaces as a partial response.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
