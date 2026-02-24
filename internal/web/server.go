// Package web implements the HTTP server for the Errata web interface.
// The frontend communicates over a single WebSocket connection per browser session.
package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
)

//go:embed static
var staticFiles embed.FS

// Server is the Errata web interface.
type Server struct {
	adapters  []models.ModelAdapter
	prefPath  string
	histPath  string
	sessionID string
	cfg       config.Config

	histMu    sync.RWMutex
	histories map[string][]models.ConversationTurn // shared across all browser connections
}

// New creates a Server. A fresh session ID is generated on each call.
// Conversation history is loaded from histPath if it exists.
func New(adapters []models.ModelAdapter, prefPath, histPath string, cfg config.Config) *Server {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	h, err := history.Load(histPath)
	if err != nil {
		log.Printf("web: could not load history: %v", err)
	}

	return &Server{
		adapters:  adapters,
		prefPath:  prefPath,
		histPath:  histPath,
		sessionID: hex.EncodeToString(b),
		cfg:       cfg,
		histories: h,
	}
}

// Start registers routes and begins serving on addr (e.g. ":8080").
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// Embedded static assets: /, /style.css, /app.js
	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	// WebSocket endpoint — one connection per browser session
	mux.HandleFunc("GET /ws", s.handleWS)

	// Stateless REST endpoints
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /api/commands", s.handleCommands)
	mux.HandleFunc("GET /api/available-models", s.handleAvailableModels)

	return http.ListenAndServe(addr, mux)
}
