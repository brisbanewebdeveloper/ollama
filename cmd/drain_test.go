package cmd

import (
	"bytes"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDrainCommandUsesPrivateSocketAndOwnership(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	t.Setenv("OLLAMA_DRAIN_SOCKET", socket)
	// Any accidental use of the public API must fail instead of reaching a server.
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	type received struct{ method, path, token string }
	requests := make(chan received, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- received{r.Method, r.URL.Path, r.Header.Get("X-Ollama-Drain-Owner")}
		w.Write([]byte("0\n"))
	})}
	go srv.Serve(ln)
	defer srv.Close()
	for _, action := range []string{"begin", "status", "resume", "recover"} {
		cmd := newDrainCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{action, "--token", "owner"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		got := <-requests
		method := http.MethodPost
		if action == "status" {
			method = http.MethodGet
		}
		if got.method != method || got.path != "/"+action || got.token != "owner" || output.String() != "0\n" {
			t.Fatalf("%s: request %+v, output %q", action, got, output.String())
		}
	}
	for _, args := range [][]string{{}, {"bogus"}, {"begin", "extra"}} {
		cmd := newDrainCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}
