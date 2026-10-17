package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func drainSocketPath() string {
	if path := os.Getenv("OLLAMA_DRAIN_SOCKET"); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ollama-admin-%d", os.Getuid()), "drain.sock")
}

func (g *drainGate) adminHandler(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	token := r.Header.Get("X-Ollama-Drain-Owner")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/begin":
		if g.owner != "" {
			http.Error(w, "another drain is already active", http.StatusConflict)
			return
		}
		if token == "" {
			token = drainToken()
		}
		g.owner = token
		fmt.Fprintln(w, g.owner)
	case r.Method == http.MethodGet && r.URL.Path == "/status", r.Method == http.MethodPost && r.URL.Path == "/resume":
		if g.owner == "" || token != g.owner {
			http.Error(w, "drain ownership mismatch", http.StatusConflict)
			return
		}
		if r.URL.Path == "/resume" {
			g.owner = ""
			fmt.Fprintln(w, "resumed")
			return
		}
		count := 0
		for _, n := range g.active {
			count += n
		}
		fmt.Fprintln(w, count)
	case r.Method == http.MethodPost && r.URL.Path == "/recover":
		// Explicit local-admin recovery for a controller killed without cleanup.
		g.owner = ""
		fmt.Fprintln(w, "resumed")
	default:
		http.NotFound(w, r)
	}
}

func (g *drainGate) startAdmin() (func(), error) {
	if runtime.GOOS == "windows" {
		return func() {}, nil
	}
	path := drainSocketPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !privateDrainDirectory(info) {
		return nil, fmt.Errorf("drain socket requires a private directory: %s", dir)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket drain path: %s", path)
		}
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("drain socket already in use: %s", path)
		}
		if !staleDrainSocket(dialErr) {
			return nil, fmt.Errorf("check existing drain socket: %w", dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return nil, err
	}
	srv := &http.Server{Handler: http.HandlerFunc(g.adminHandler), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	return func() { srv.Close() }, nil
}

// DrainControl talks only to the private local socket, never OLLAMA_HOST.
func DrainControl(ctx context.Context, action, token string) (string, error) {
	method := http.MethodPost
	if action == "status" {
		method = http.MethodGet
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", drainSocketPath())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, "http://local/"+action, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Ollama-Drain-Owner", token)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("drain control unavailable (requires a drain-enabled server): %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("drain %s: %s", action, result)
	}
	if action == "status" {
		if n, err := strconv.Atoi(result); err != nil || n < 0 {
			return "", fmt.Errorf("invalid drain count: %q", result)
		}
	}
	return result, nil
}
