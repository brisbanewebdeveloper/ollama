package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
)

const drainTicketHeader = "X-Ollama-Drain-Ticket"

type drainContextKey struct{}

// Admission and drain transitions share a lock so zero active requests is
// authoritative: no new work can enter between the check and Docker shutdown.
type drainGate struct {
	mu     sync.Mutex
	owner  string
	active map[string]int
}

func drainToken() string {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(token[:])
}

func (g *drainGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read-only probes remain usable while draining. Proxy requests are
		// tracked regardless of method because their upstream work may outlive us.
		work := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		work = work || strings.HasPrefix(r.URL.Path, "/api/codex/")
		if !work {
			r.Header.Del(drainTicketHeader)
			next.ServeHTTP(w, r)
			return
		}

		g.mu.Lock()
		if g.active == nil {
			g.active = make(map[string]int)
		}
		ticket := r.Header.Get(drainTicketHeader)
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		internal := ip != nil && ip.IsLoopback() && g.active[ticket] > 0
		r.Header.Del(drainTicketHeader)
		if !internal && g.owner != "" {
			g.mu.Unlock()
			w.Header().Set("Retry-After", "5")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"Ollama is draining for restart; retry later"}`))
			return
		}
		if !internal {
			ticket = drainToken()
		}
		g.active[ticket]++
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.active[ticket]--
			if g.active[ticket] == 0 {
				delete(g.active, ticket)
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), drainContextKey{}, ticket)))
	})
}

// Only the trusted local proxy attaches tickets, after stripping client headers.
// A ticket is valid only while its originating request is still active.
func (g *drainGate) prepareLocalRequest(r *http.Request) {
	r.Header.Del(drainTicketHeader)
	if ticket, ok := r.Context().Value(drainContextKey{}).(string); ok {
		r.Header.Set(drainTicketHeader, ticket)
	}
}
