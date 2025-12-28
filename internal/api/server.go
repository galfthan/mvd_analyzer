// Package api provides the HTTP REST API for the MVD analyzer.
package api

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
)

// Server handles HTTP requests for the MVD analyzer
type Server struct {
	mux      *http.ServeMux
	staticFS embed.FS
}

// NewServer creates a new API server
func NewServer(staticFS embed.FS) *Server {
	s := &Server{
		mux:      http.NewServeMux(),
		staticFS: staticFS,
	}
	s.setupRoutes()
	return s
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// API routes - use standard pattern without method prefix for compatibility
	s.mux.HandleFunc("/api/analyze", s.handleAnalyze)
	s.mux.HandleFunc("/api/analyses", s.handleListAnalyses)
	s.mux.HandleFunc("/api/analyses/", s.handleGetAnalysisWrapper)

	// Serve static files for dashboard
	staticContent, err := fs.Sub(s.staticFS, "static")
	if err != nil {
		// Fallback - no static files
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><h1>MVD Analyzer</h1><p>Static files not found</p></body></html>"))
		})
		return
	}

	// Serve static files
	fileServer := http.FileServer(http.FS(staticContent))
	s.mux.Handle("/", fileServer)
}

// handleGetAnalysisWrapper extracts ID from path and calls handleGetAnalysis
func (s *Server) handleGetAnalysisWrapper(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path: /api/analyses/{id}
	id := r.URL.Path[len("/api/analyses/"):]
	if id == "" {
		http.Error(w, "Missing analysis ID", http.StatusBadRequest)
		return
	}

	analysesMu.RLock()
	result, ok := analyses[id]
	analysesMu.RUnlock()

	if !ok {
		http.Error(w, "Analysis not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts the HTTP server
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s)
}
