package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/openai"
)

func TestResponsesMiddlewareAcceptsDeferredToolExchange(t *testing.T) {
	var captured *api.ChatRequest
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ResponsesMiddleware(), captureRequestMiddleware(&captured))
	router.POST("/v1/responses", func(c *gin.Context) { c.Status(http.StatusOK) })

	body := `{
		"model":"test-model",
		"input":[
			{"type":"additional_tools","role":"developer","tools":[{"type":"tool_search","execution":"client","description":"Discover tools","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]},
			{"type":"message","role":"user","content":"initialize"},
			{"type":"tool_search_call","call_id":"search-1","execution":"client","arguments":{"query":"bootstrap"}},
			{"type":"tool_search_output","call_id":"search-1","status":"completed","execution":"client","tools":[{"type":"namespace","name":"mcp__example","description":"Example tools","tools":[{"type":"function","name":"session_bootstrap","description":"Initialize","strict":false,"parameters":{"type":"object","properties":{}}}]}]}
		]
	}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if captured == nil {
		t.Fatal("converted chat request was not captured")
	}
	var found bool
	for _, tool := range captured.Tools {
		if tool.Function.Name == "mcp__example.session_bootstrap" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("discovered function was not callable: %#v", captured.Tools)
	}
}

func TestWebSearchMixedOutputPreservesDeferredToolItemTypes(t *testing.T) {
	description := "Discover tools"
	request := openai.ResponsesRequest{Tools: []openai.ResponsesTool{
		{Type: "tool_search", Execution: "client", Description: &description, Parameters: map[string]any{"type": "object"}},
		{Type: "namespace", Name: "mcp__example", Tools: []openai.ResponsesTool{{Type: "function", Name: "session_bootstrap"}}},
	}}
	var searchArgs, functionArgs api.ToolCallFunctionArguments
	if err := json.Unmarshal([]byte(`{"query":"bootstrap"}`), &searchArgs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"prompt":"hello"}`), &functionArgs); err != nil {
		t.Fatal(err)
	}
	items := buildResponsesWebSearchOutput(nil, "", "", nil, []api.ToolCall{
		{ID: "search-1", Function: api.ToolCallFunction{Name: "tool_search", Arguments: searchArgs}},
		{ID: "call-1", Function: api.ToolCallFunction{Name: "mcp__example.session_bootstrap", Arguments: functionArgs}},
	}, request)

	if len(items) != 2 || items[0].Type != "tool_search_call" {
		t.Fatalf("mixed search output = %#v", items)
	}
	if items[1].Type != "function_call" || items[1].Namespace != "mcp__example" || items[1].Name != "session_bootstrap" {
		t.Fatalf("mixed namespaced output = %#v", items[1])
	}
}
