// Package web implements the HTTP server for the Errata web interface.
// The frontend communicates over a single WebSocket connection per browser session.
package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/tools"
)

//go:embed static
var staticFiles embed.FS

// Server is the Errata web interface.
type Server struct {
	adapters        []models.ModelAdapter
	prefPath        string
	histPath        string
	sessionID       string
	cfg             config.Config
	rec             *recipe.Recipe // recipe settings (nil = use defaults)
	startupWarnings []string       // sent to each browser on WS connect

	// MCP tool definitions and dispatchers (nil if no MCP servers configured)
	mcpDefs        []tools.ToolDef
	mcpDispatchers map[string]tools.MCPDispatcher

	histMu     sync.RWMutex
	histories  map[string][]models.ConversationTurn // shared across all browser connections
	httpServer *http.Server                         // set by Start; used by Shutdown
}

// New creates a Server. A fresh session ID is generated on each call.
// Conversation history is loaded from histPath if it exists.
// startupWarnings are sent to each browser client on WebSocket connect.
func New(adapters []models.ModelAdapter, prefPath, histPath string, cfg config.Config, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, startupWarnings []string, rec *recipe.Recipe) *Server {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	h, err := history.Load(histPath)
	if err != nil {
		log.Printf("web: could not load history: %v", err)
	}

	return &Server{
		adapters:        adapters,
		prefPath:        prefPath,
		histPath:        histPath,
		sessionID:       hex.EncodeToString(b),
		cfg:             cfg,
		rec:             rec,
		histories:       h,
		mcpDefs:         mcpDefs,
		mcpDispatchers:  mcpDispatchers,
		startupWarnings: startupWarnings,
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
	mux.HandleFunc("GET /api/tools", s.handleToolsList)

	s.httpServer = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server, allowing active WebSocket
// connections to close (which triggers per-connection context cancellation and
// checkpoint saves for any active runs).
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
