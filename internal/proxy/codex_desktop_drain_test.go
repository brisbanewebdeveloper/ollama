package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexDesktopDrainPreparationOnlyForLocalUpstream(t *testing.T) {
	type admissionKey struct{}
	for _, local := range []bool{true, false} {
		t.Run(map[bool]string{true: "local", false: "native"}[local], func(t *testing.T) {
			var received string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = r.Header.Get("X-Test-Admission")
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer upstream.Close()
			called := false
			h, err := NewCodexDesktop(CodexDesktopConfig{
				OllamaURL: upstream.URL, ChatGPTURL: upstream.URL, OpenAIURL: upstream.URL,
				RoutingCatalogPath: writeCatalog(t, "local-model"),
				LocalRequest: func(r *http.Request) {
					called = true
					if ticket, _ := r.Context().Value(admissionKey{}).(string); ticket != "" {
						r.Header.Set("X-Test-Admission", ticket)
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			model := "native-model"
			if local {
				model = "local-model"
			}
			r := httptest.NewRequest(http.MethodPost, CodexDesktopPathPrefix+"/v1/responses", strings.NewReader(`{"model":"`+model+`","input":"hello","stream":true}`))
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Authorization", "Bearer test")
			r = r.WithContext(context.WithValue(r.Context(), admissionKey{}, "admitted"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("proxy: %d %s", w.Code, w.Body.String())
			}
			if called != local {
				t.Fatalf("local preparation called = %v", called)
			}
			if local && received != "admitted" {
				t.Fatalf("context admission lost: %q", received)
			}
			if !local && received != "" {
				t.Fatal("admission leaked to native upstream")
			}
		})
	}
}
