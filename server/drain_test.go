package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func drainAdminCall(g *drainGate, action, token string) *httptest.ResponseRecorder {
	method := http.MethodPost
	if action == "status" {
		method = http.MethodGet
	}
	r := httptest.NewRequest(method, "/"+action, nil)
	r.Header.Set("X-Ollama-Drain-Owner", token)
	w := httptest.NewRecorder()
	g.adminHandler(w, r)
	return w
}

func waitDrainEvent(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach expected stage")
	}
}

func TestDrainPreservesAdmittedProxyStreamAndRejectsNewWork(t *testing.T) {
	g := &drainGate{}
	entered, proceed, streaming, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var proceedOnce, finishOnce sync.Once
	var baseURL, ticket string
	client := &http.Client{Timeout: 5 * time.Second}
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/codex/") {
			ticket = r.Context().Value(drainContextKey{}).(string)
			close(entered)
			<-proceed
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, baseURL+"/api/chat", nil)
			g.prepareLocalRequest(req)
			resp, err := client.Do(req)
			if err != nil {
				http.Error(w, err.Error(), 502)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return
		}
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		close(streaming)
		<-finish
		io.WriteString(w, "last\n")
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	// Cleanup must run before Close waits for blocked handlers.
	defer proceedOnce.Do(func() { close(proceed) })
	defer finishOnce.Do(func() { close(finish) })
	baseURL = srv.URL
	type result struct {
		code int
		body string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := client.Post(baseURL+"/api/codex/v1/responses", "application/json", bytes.NewBufferString("{}"))
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		resultCh <- result{resp.StatusCode, string(body), err}
	}()
	waitDrainEvent(t, entered)
	begin := drainAdminCall(g, "begin", "controller")
	if begin.Code != 200 {
		t.Fatal(begin.Body.String())
	}
	if got := drainAdminCall(g, "status", "controller").Body.String(); got != "1\n" {
		t.Fatalf("queued request count = %q", got)
	}
	for _, path := range []string{"/api/chat", "/v1/responses", "/v1/messages", "/api/embed", "/api/create", "/api/codex/v1/responses"} {
		req, _ := http.NewRequest(http.MethodPost, baseURL+path, nil)
		req.Header.Set(drainTicketHeader, "forged")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
	proceedOnce.Do(func() { close(proceed) })
	waitDrainEvent(t, streaming)
	if got := drainAdminCall(g, "status", "controller").Body.String(); got != "2\n" {
		t.Fatalf("stream/proxy count = %q", got)
	}
	finishOnce.Do(func() { close(finish) })
	select {
	case got := <-resultCh:
		if got.err != nil || got.code != 200 || got.body != "first\nlast\n" {
			t.Fatalf("stream result: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish")
	}
	if got := drainAdminCall(g, "status", "controller").Body.String(); got != "0\n" {
		t.Fatalf("finished count = %q", got)
	}
	// A ticket cannot admit further work after its parent has finished.
	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set(drainTicketHeader, ticket)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("replayed ticket accepted: %d", w.Code)
	}
}

func TestDrainOwnershipResumeProbesAndCancellation(t *testing.T) {
	g := &drainGate{}
	if w := drainAdminCall(g, "begin", "owner"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, action := range []string{"begin", "status", "resume"} {
		if w := drainAdminCall(g, action, "other"); w.Code != 409 {
			t.Fatalf("%s accepted other controller", action)
		}
	}
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/ps", nil))
	if w.Code != 204 {
		t.Fatal("probe blocked")
	}
	if w := drainAdminCall(g, "resume", "owner"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/chat", nil))
	if w.Code != 204 {
		t.Fatal("admission not restored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h = g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/chat", nil).WithContext(ctx))
	drainAdminCall(g, "begin", "owner")
	if got := drainAdminCall(g, "status", "owner").Body.String(); got != "0\n" {
		t.Fatalf("cancelled request retained: %q", got)
	}
}

func TestDrainPrivateSocketControlAndRecovery(t *testing.T) {
	t.Setenv("OLLAMA_DRAIN_SOCKET", filepath.Join(t.TempDir(), "private", "drain.sock"))
	g := &drainGate{}
	closeAdmin, err := g.startAdmin()
	if err != nil {
		t.Fatal(err)
	}
	defer closeAdmin()
	ctx := context.Background()
	token, err := DrainControl(ctx, "begin", "")
	if err != nil || len(token) != 64 {
		t.Fatalf("begin: %q, %v", token, err)
	}
	if _, err := DrainControl(ctx, "begin", ""); err == nil {
		t.Fatal("second drain acquired ownership")
	}
	if _, err := g.startAdmin(); err == nil {
		t.Fatal("second admin replaced active socket")
	}
	if got, err := DrainControl(ctx, "status", token); err != nil || got != "0" {
		t.Fatalf("status: %q, %v", got, err)
	}
	if _, err := DrainControl(ctx, "recover", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := DrainControl(ctx, "status", token); err == nil {
		t.Fatal("old ownership valid after recovery")
	}
	closeAdmin()
	if _, err := DrainControl(ctx, "begin", ""); err == nil {
		t.Fatal("unavailable server accepted drain")
	}
}
