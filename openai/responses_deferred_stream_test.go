package openai

import (
	"testing"

	"github.com/ollama/ollama/api"
)

func TestDeferredToolCallsStreaming(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		arguments     string
		wantType      string
		wantNamespace string
		wantName      string
	}{
		{
			name:      "tool search call",
			toolName:  "tool_search",
			arguments: `{"query":"session bootstrap"}`,
			wantType:  "tool_search_call",
		},
		{
			name:          "discovered namespaced function",
			toolName:      "mcp__example.session_bootstrap",
			arguments:     `{"prompt":"hello"}`,
			wantType:      "function_call",
			wantNamespace: "mcp__example",
			wantName:      "session_bootstrap",
		},
		{
			name:      "ordinary function regression",
			toolName:  "lookup",
			arguments: `{}`,
			wantType:  "function_call",
			wantName:  "lookup",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustDeferredRequest(t)
			converter := NewResponsesStreamConverter("resp-1", "msg-1", "test-model", request)
			events := converter.Process(api.ChatResponse{
				Done: true,
				Message: api.Message{ToolCalls: []api.ToolCall{{
					ID: "call-1",
					Function: api.ToolCallFunction{
						Name:      test.toolName,
						Arguments: mustToolArguments(t, test.arguments),
					},
				}}},
			})

			item := streamedDoneItem(t, events, test.wantType)
			if item["call_id"] != "call-1" {
				t.Fatalf("call_id = %#v", item["call_id"])
			}
			if test.wantNamespace != "" && item["namespace"] != test.wantNamespace {
				t.Fatalf("namespace = %#v, want %q", item["namespace"], test.wantNamespace)
			}
			if test.wantName != "" && item["name"] != test.wantName {
				t.Fatalf("name = %#v, want %q", item["name"], test.wantName)
			}
			if test.wantType == "tool_search_call" {
				if item["execution"] != "client" {
					t.Fatalf("execution = %#v", item["execution"])
				}
				arguments, ok := item["arguments"].(map[string]any)
				if !ok || arguments["query"] != "session bootstrap" {
					t.Fatalf("arguments = %#v", item["arguments"])
				}
				for _, event := range events {
					if event.Event == "response.function_call_arguments.delta" {
						t.Fatal("tool_search_call must not be emitted as a function_call delta")
					}
				}
			}
			assertCompletedOutputContains(t, events, test.wantType)
		})
	}
}

func streamedDoneItem(t *testing.T, events []ResponsesStreamEvent, itemType string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event.Event != "response.output_item.done" {
			continue
		}
		data := event.Data.(map[string]any)
		item, ok := data["item"].(map[string]any)
		if ok && item["type"] == itemType {
			return item
		}
	}
	t.Fatalf("no response.output_item.done event for %q: %#v", itemType, events)
	return nil
}

func assertCompletedOutputContains(t *testing.T, events []ResponsesStreamEvent, itemType string) {
	t.Helper()
	for _, event := range events {
		if event.Event != "response.completed" {
			continue
		}
		data := event.Data.(map[string]any)
		response := data["response"].(map[string]any)
		for _, raw := range response["output"].([]any) {
			if raw.(map[string]any)["type"] == itemType {
				return
			}
		}
	}
	t.Fatalf("completed response does not contain %q", itemType)
}
