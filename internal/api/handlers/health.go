package handlers

import (
	"net/http"
	"sync/atomic"
)

// HealthHandler serves /healthz (liveness) and /readyz (readiness).
//
// Liveness reflects only that the process is up and the listener is open;
// it returns HTTP 200 unconditionally while the listener accepts
// connections (REQ A-5.1, REQ A-5.8 — /healthz keeps returning 200 during
// shutdown until the listener closes).
//
// Readiness reflects the union of readiness signals wired by main(). Phase
// 1 wires three flags: StartupCompleted (true once the listener is bound),
// LanguageRegistryLoaded (true once internal/registry has loaded a valid
// YAML registry — REQ A-5.2, REQ A-5.3), and ShutdownInProgress (true
// once a termination signal is received — REQ A-5.8). Later phases extend
// the set with Worker_Pool and NsJail readiness; the response body's
// failed_components array enumerates every false flag (REQ A-5.5,
// Property 5).
type HealthHandler struct {
	startupCompleted        *atomic.Bool
	languageRegistryLoaded  *atomic.Bool
	shutdownInProgress      *atomic.Bool
}

// NewHealthHandler constructs a HealthHandler bound to the given flags.
//
// All pointers must be non-nil; callers own the underlying values and
// flip them as the process lifecycle progresses.
func NewHealthHandler(startupCompleted, languageRegistryLoaded, shutdownInProgress *atomic.Bool) *HealthHandler {
	return &HealthHandler{
		startupCompleted:       startupCompleted,
		languageRegistryLoaded: languageRegistryLoaded,
		shutdownInProgress:     shutdownInProgress,
	}
}

// Live serves GET /healthz.
//
// Returns HTTP 200 with body {"status":"ok"} whenever the process can
// answer at all.
func (h *HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// Ready serves GET /readyz.
//
// Returns HTTP 200 with body {"ready":true} when every readiness flag is
// set, otherwise HTTP 503 with body
// {"ready":false,"failed_components":[{"component":"...","reason":"..."}]}
// enumerating every flag that is currently false (Property 5).
func (h *HealthHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	failed := h.failedComponents()
	if len(failed) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ready": true})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"ready":             false,
		"failed_components": failed,
	})
}

// failedComponents returns one entry per readiness flag that is currently
// false. Order is fixed for deterministic test assertions.
func (h *HealthHandler) failedComponents() []map[string]string {
	var out []map[string]string
	if !h.startupCompleted.Load() {
		out = append(out, map[string]string{
			"component": "startup",
			"reason":    "startup_not_completed",
		})
	}
	if !h.languageRegistryLoaded.Load() {
		out = append(out, map[string]string{
			"component": "Language_Registry",
			"reason":    "registry_not_loaded",
		})
	}
	if h.shutdownInProgress.Load() {
		out = append(out, map[string]string{
			"component": "shutdown",
			"reason":    "shutdown_in_progress",
		})
	}
	return out
}
