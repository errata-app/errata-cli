package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

// Login runs the interactive OAuth login flow:
// 1. Starts a local HTTP server on an available port
// 2. Opens the browser to the errata.app OAuth page
// 3. Waits for the callback with the token
// 4. Saves the token to disk
func Login() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("could not start local server: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener address type")
	}
	port := tcpAddr.Port

	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			errCh <- fmt.Errorf("callback received without token")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><h2>Logged in! You can close this tab.</h2></body></html>`)
		tokenCh <- token
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	loginURL := AuthLoginURL(port)
	fmt.Printf("Opening browser to: %s\n", loginURL)
	if browserErr := openBrowser(loginURL); browserErr != nil {
		fmt.Printf("Could not open browser. Please visit:\n  %s\n", loginURL)
	}

	// Wait for token or timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	select {
	case token := <-tokenCh:
		_ = srv.Shutdown(context.Background())
		if saveErr := SaveToken(token); saveErr != nil {
			return fmt.Errorf("login succeeded but could not save token: %w", saveErr)
		}
		return nil
	case err := <-errCh:
		_ = srv.Shutdown(context.Background())
		return err
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return fmt.Errorf("login timed out (5 minutes)")
	}
}

// openBrowser opens the given URL in the default browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}
