// Package api provides the HTTP REST API for the MVD analyzer.
package api

import (
	"embed"
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
	// API routes
	s.mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/analyses", s.handleListAnalyses)
	s.mux.HandleFunc("GET /api/analyses/{id}", s.handleGetAnalysis)

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

// ServeHTTP implements http.Handler
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts the HTTP server
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s)
}
