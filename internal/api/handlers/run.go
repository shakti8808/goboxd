package handlers

import "net/http"

// RunHandler serves POST /run.
//
// Phase 1 ships a stub that returns HTTP 503 with body
// {"error":"not_implemented"} so the architecture's status-code partition
// (Property 3) holds even though the sandbox pipeline is not yet wired.
// The full handler — JSON decode → Security_Validator → Worker_Pool →
// Sandbox_Runner → response — is delivered in Phase 2 (see
// .kiro/specs/goboxd-implementation/tasks.md task 21).
type RunHandler struct{}

// NewRunHandler returns the Phase 1 stub.
func NewRunHandler() *RunHandler { return &RunHandler{} }

// Run serves the /run request.
func (h *RunHandler) Run(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": "not_implemented",
	})
}
