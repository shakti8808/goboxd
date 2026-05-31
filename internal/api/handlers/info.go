package handlers

import "net/http"

// InfoHandler serves GET /info.
//
// The response body is a JSON object with exactly the field set
// {service_name, version, languages, worker_pool_size}. Per REQ A-6.2 the
// body MUST NOT include secrets, file paths, or any value derived from a
// Code_Submission; the InfoHandler therefore takes its inputs at
// construction time and never reads the request body.
type InfoHandler struct {
	serviceName    string
	version        string
	workerPoolSize int
	languages      []string
}

// NewInfoHandler constructs an InfoHandler.
//
// languages may be nil; the handler emits an empty JSON array in that
// case, matching REQ A-6.1 ("array of strings (which MAY be empty)"). The
// constructor copies the slice header so callers may discard their
// reference safely.
func NewInfoHandler(serviceName, version string, workerPoolSize int, languages []string) *InfoHandler {
	if languages == nil {
		languages = []string{}
	}
	return &InfoHandler{
		serviceName:    serviceName,
		version:        version,
		workerPoolSize: workerPoolSize,
		languages:      languages,
	}
}

// Info serves the /info request.
func (h *InfoHandler) Info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service_name":     h.serviceName,
		"version":          h.version,
		"languages":        h.languages,
		"worker_pool_size": h.workerPoolSize,
	})
}
