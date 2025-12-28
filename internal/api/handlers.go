package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/mvd-analyzer/internal/analyzer"
)

var (
	analyses   = make(map[string]*analyzer.Result)
	analysesMu sync.RWMutex
)

// handleAnalyze handles POST /api/analyze
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form
	err := r.ParseMultipartForm(100 << 20) // 100MB max
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file uploaded: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save to temp file
	tempDir := os.TempDir()
	tempFile, err := os.CreateTemp(tempDir, "mvd-*.mvd")
	if err != nil {
		http.Error(w, "Failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		http.Error(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tempFile.Close()

	// Analyze the file
	registry := analyzer.NewDefaultRegistry()
	result, err := registry.Analyze(tempFile.Name())
	if err != nil {
		http.Error(w, "Analysis failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Use original filename
	result.FilePath = header.Filename

	// Generate ID and store result
	id := generateID()
	analysesMu.Lock()
	analyses[id] = result
	analysesMu.Unlock()

	// Return result with ID
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"result": result,
	})
}

// handleListAnalyses handles GET /api/analyses
func (s *Server) handleListAnalyses(w http.ResponseWriter, r *http.Request) {
	analysesMu.RLock()
	defer analysesMu.RUnlock()

	list := make([]map[string]interface{}, 0, len(analyses))
	for id, result := range analyses {
		list = append(list, map[string]interface{}{
			"id":       id,
			"filePath": result.FilePath,
			"duration": result.Duration,
			"map":      result.Match.Map,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// handleGetAnalysis handles GET /api/analyses/{id}
func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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

// handleAnalyzeFile handles analyzing a file from filesystem (for CLI -> web handoff)
func AnalyzeFile(filePath string) (string, *analyzer.Result, error) {
	registry := analyzer.NewDefaultRegistry()
	result, err := registry.Analyze(filePath)
	if err != nil {
		return "", nil, err
	}

	result.FilePath = filepath.Base(filePath)

	id := generateID()
	analysesMu.Lock()
	analyses[id] = result
	analysesMu.Unlock()

	return id, result, nil
}

func generateID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
