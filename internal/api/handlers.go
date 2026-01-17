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
	"github.com/mvd-analyzer/internal/hub"
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

// handleHubLoad handles POST /api/hub/load
// Downloads and analyzes a demo from QuakeWorld Hub
func (s *Server) handleHubLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Input == "" {
		http.Error(w, "Missing input (game ID or URL)", http.StatusBadRequest)
		return
	}

	// Parse game ID
	gameID, err := hub.ParseGameID(req.Input)
	if err != nil {
		http.Error(w, "Invalid game ID: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Fetch game info
	client := hub.NewClient()
	game, err := client.GetGame(gameID)
	if err != nil {
		http.Error(w, "Failed to fetch game: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Download demo to temp file
	tempDir := os.TempDir()
	filename := game.GenerateDemoFilename()
	tempPath := filepath.Join(tempDir, filename)

	if err := client.DownloadDemo(game, tempPath); err != nil {
		http.Error(w, "Failed to download demo: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempPath)

	// Analyze the demo
	registry := analyzer.NewDefaultRegistry()
	result, err := registry.Analyze(tempPath)
	if err != nil {
		http.Error(w, "Analysis failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Use descriptive filename
	result.FilePath = filename

	// Store result
	id := generateID()
	analysesMu.Lock()
	analyses[id] = result
	analysesMu.Unlock()

	// Return result with ID and hub info
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     id,
		"result": result,
		"hub": map[string]interface{}{
			"gameId":    gameID,
			"viewerUrl": game.GetViewerURLWithTime(0, 0),
		},
	})
}
