package openai

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

const deferredResponsesFixture = `{
	"model":"test-model",
	"input":[
		{"type":"additional_tools","role":"developer","tools":[
			{"type":"function","name":"lookup","description":"Ordinary function","strict":false,"parameters":{"type":"object","properties":{}}},
			{"type":"tool_search","execution":"client","description":"Discover deferred tools","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}
		]},
		{"type":"message","role":"user","content":"initialize the session"},
		{"type":"tool_search_call","call_id":"search-1","status":"completed","execution":"client","arguments":{"query":"session bootstrap","limit":1}},
		{"type":"tool_search_output","call_id":"search-1","status":"completed","execution":"client","tools":[
			{"type":"namespace","name":"mcp__example","description":"Example MCP tools","tools":[
				{"type":"function","name":"session_bootstrap","description":"Initialize a session","strict":false,"defer_loading":true,"parameters":{"type":"object","additionalProperties":false,"properties":{"prompt":{"type":"string","minLength":1}},"required":["prompt"]}}
			]}
		]}
	]
}`

func TestDeferredResponsesInputItemsDecodeAndRoundTrip(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(deferredResponsesFixture), &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Input.Items) != 4 {
		t.Fatalf("input item count = %d, want 4", len(request.Input.Items))
	}
	if _, ok := request.Input.Items[0].(ResponsesAdditionalTools); !ok {
		t.Fatalf("input[0] type = %T, want ResponsesAdditionalTools", request.Input.Items[0])
	}
	call, ok := request.Input.Items[2].(ResponsesToolSearchCall)
	if !ok {
		t.Fatalf("input[2] type = %T, want ResponsesToolSearchCall", request.Input.Items[2])
	}
	if call.CallID == nil || *call.CallID != "search-1" || call.Arguments["query"] != "session bootstrap" {
		t.Fatalf("decoded tool search call = %#v", call)
	}
	output, ok := request.Input.Items[3].(ResponsesToolSearchOutput)
	if !ok {
		t.Fatalf("input[3] type = %T, want ResponsesToolSearchOutput", request.Input.Items[3])
	}
	if len(output.Tools) != 1 || output.Tools[0].Name != "mcp__example" {
		t.Fatalf("decoded tool search output = %#v", output)
	}

	for _, index := range []int{0, 2, 3} {
		encoded, err := json.Marshal(request.Input.Items[index])
		if err != nil {
			t.Fatal(err)
		}
		var roundTripped map[string]any
		if err := json.Unmarshal(encoded, &roundTripped); err != nil {
			t.Fatal(err)
		}
		if roundTripped["type"] == "" {
			t.Fatalf("input[%d] lost its discriminator: %s", index, encoded)
		}
		tools, _ := roundTripped["tools"].([]any)
		switch index {
		case 0:
			if roundTripped["role"] != "developer" || len(tools) != 2 {
				t.Fatalf("additional_tools changed during round trip: %s", encoded)
			}
		case 2:
			arguments := roundTripped["arguments"].(map[string]any)
			if roundTripped["call_id"] != "search-1" || arguments["query"] != "session bootstrap" {
				t.Fatalf("tool_search_call changed during round trip: %s", encoded)
			}
		case 3:
			if roundTripped["call_id"] != "search-1" || len(tools) != 1 {
				t.Fatalf("tool_search_output changed during round trip: %s", encoded)
			}
		}
	}
}

func TestDeferredToolSearchOutputRegistersCompleteNamespacedFunction(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(deferredResponsesFixture), &request); err != nil {
		t.Fatal(err)
	}
	chat, err := FromResponsesRequest(request)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"lookup", "tool_search", "mcp__example.session_bootstrap"} {
		if findChatTool(chat.Tools, name) == nil {
			t.Errorf("model-visible tool %q was not registered", name)
		}
	}
	search := findChatTool(chat.Tools, "tool_search")
	if search == nil || search.Function.Description != "Discover deferred tools" || len(search.Function.Parameters.Required) != 1 || search.Function.Parameters.Required[0] != "query" {
		t.Fatalf("tool_search schema = %#v", search)
	}
	discovered := findChatTool(chat.Tools, "mcp__example.session_bootstrap")
	if discovered == nil {
		t.Fatal("discovered tool not found")
	}
	if discovered.Function.Description != "Initialize a session" {
		t.Fatalf("description = %q", discovered.Function.Description)
	}
	parameters, err := json.Marshal(discovered.Function.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties was not preserved: %s", parameters)
	}
	properties := schema["properties"].(map[string]any)
	prompt := properties["prompt"].(map[string]any)
	if prompt["minLength"] != float64(1) {
		t.Fatalf("nested schema keyword was not preserved: %s", parameters)
	}

	if len(chat.Messages) != 3 || len(chat.Messages[1].ToolCalls) != 1 {
		t.Fatalf("converted search history = %#v", chat.Messages)
	}
	if chat.Messages[1].ToolCalls[0].Function.Name != "tool_search" || chat.Messages[2].ToolCallID != "search-1" {
		t.Fatalf("tool search call/output pairing was not preserved: %#v", chat.Messages)
	}
}

func TestDeferredNamespacedFunctionCallUsesResponsesIdentity(t *testing.T) {
	request := mustDeferredRequest(t)
	response := ToResponse("test-model", "resp-1", "msg-1", api.ChatResponse{
		CreatedAt: time.Unix(1, 0),
		Message: api.Message{ToolCalls: []api.ToolCall{{
			ID: "call-1",
			Function: api.ToolCallFunction{
				Name:      "mcp__example.session_bootstrap",
				Arguments: mustToolArguments(t, `{"prompt":"hello"}`),
			},
		}}},
	}, request)

	if len(response.Output) != 1 {
		t.Fatalf("output count = %d", len(response.Output))
	}
	item := response.Output[0]
	if item.Type != "function_call" || item.Namespace != "mcp__example" || item.Name != "session_bootstrap" {
		t.Fatalf("namespaced output item = %#v", item)
	}
	if item.Arguments != `{"prompt":"hello"}` {
		t.Fatalf("arguments = %#v", item.Arguments)
	}
}

func TestToolSearchCallUsesResponsesItemNonStreaming(t *testing.T) {
	request := mustDeferredRequest(t)
	response := ToResponse("test-model", "resp-1", "msg-1", api.ChatResponse{
		CreatedAt: time.Unix(1, 0),
		Message: api.Message{ToolCalls: []api.ToolCall{{
			ID: "search-2",
			Function: api.ToolCallFunction{
				Name:      "tool_search",
				Arguments: mustToolArguments(t, `{"query":"bootstrap","limit":1}`),
			},
		}}},
	}, request)

	item := response.Output[0]
	arguments, ok := item.Arguments.(map[string]any)
	if item.Type != "tool_search_call" || item.CallID != "search-2" || item.Execution != "client" || !ok {
		t.Fatalf("tool search output item = %#v", item)
	}
	if arguments["query"] != "bootstrap" || arguments["limit"] != float64(1) {
		t.Fatalf("tool search arguments = %#v", arguments)
	}
}

func TestNamespacedFunctionCallHistoryRoundTripsToQualifiedChatName(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{
		"model":"test-model",
		"input":[
			{"type":"function_call","call_id":"call-1","namespace":"mcp__example","name":"session_bootstrap","arguments":"{\"prompt\":\"hello\"}"},
			{"type":"function_call_output","call_id":"call-1","output":"initialized"}
		]
	}`), &request); err != nil {
		t.Fatal(err)
	}
	chat, err := FromResponsesRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 2 || chat.Messages[0].ToolCalls[0].Function.Name != "mcp__example.session_bootstrap" {
		t.Fatalf("namespaced call history = %#v", chat.Messages)
	}
	if chat.Messages[1].ToolCallID != "call-1" {
		t.Fatalf("function output was not paired: %#v", chat.Messages[1])
	}
}

func TestServerExecutedToolSearchHistoryIsMetadata(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{
		"model":"test-model",
		"input":[
			{"type":"tool_search_call","call_id":null,"status":"completed","execution":"server","arguments":{"paths":["example"]}},
			{"type":"tool_search_output","call_id":null,"status":"completed","execution":"server","tools":[{"type":"function","name":"loaded","description":"Loaded server tool","strict":false,"parameters":{"type":"object","properties":{}}}]}
		]
	}`), &request); err != nil {
		t.Fatal(err)
	}
	chat, err := FromResponsesRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 0 {
		t.Fatalf("server search metadata became chat history: %#v", chat.Messages)
	}
	if findChatTool(chat.Tools, "loaded") == nil {
		t.Fatal("tool supplied by server search output was not registered")
	}
}

func TestDeferredResponsesMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"string arguments", `{"model":"m","input":[{"type":"tool_search_call","call_id":"s","execution":"client","arguments":"{}"}]}`, "arguments must be an object"},
		{"tools object", `{"model":"m","input":[{"type":"tool_search_output","call_id":"s","status":"completed","execution":"client","tools":{}}]}`, "cannot unmarshal object"},
		{"missing client call id", `{"model":"m","input":[{"type":"tool_search_call","execution":"client","arguments":{}}]}`, "requires a non-empty call_id"},
		{"unknown execution", `{"model":"m","input":[{"type":"tool_search_call","call_id":"s","execution":"remote","arguments":{}}]}`, "execution must be"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request ResponsesRequest
			err := json.Unmarshal([]byte(test.body), &request)
			if err == nil {
				_, err = FromResponsesRequest(request)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func mustDeferredRequest(t *testing.T) ResponsesRequest {
	t.Helper()
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(deferredResponsesFixture), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func mustToolArguments(t *testing.T, value string) api.ToolCallFunctionArguments {
	t.Helper()
	var arguments api.ToolCallFunctionArguments
	if err := json.Unmarshal([]byte(value), &arguments); err != nil {
		t.Fatal(err)
	}
	return arguments
}

func findChatTool(tools []api.Tool, name string) *api.Tool {
	for i := range tools {
		if tools[i].Function.Name == name {
			return &tools[i]
		}
	}
	return nil
}
