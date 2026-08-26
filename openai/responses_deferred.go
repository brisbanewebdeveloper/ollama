package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ollama/ollama/api"
)

// ResponsesAdditionalTools carries request-scoped tool declarations in the
// input stream. Codex uses this item for its Responses Lite transport.
type ResponsesAdditionalTools struct {
	ID    string          `json:"id,omitempty"`
	Type  string          `json:"type"` // always "additional_tools"
	Role  string          `json:"role"`
	Tools []ResponsesTool `json:"tools"`
}

func (ResponsesAdditionalTools) responsesInputItem() {}

// ResponsesToolSearchCall represents a deferred-tool discovery call. Client
// executions have a call ID so the following tool_search_output can be paired
// with them; legacy server executions may use a null call ID.
type ResponsesToolSearchCall struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"` // always "tool_search_call"
	CallID    *string        `json:"call_id"`
	Status    string         `json:"status,omitempty"`
	Execution string         `json:"execution"`
	Arguments map[string]any `json:"arguments"`
}

func (c *ResponsesToolSearchCall) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID        string          `json:"id,omitempty"`
		Type      string          `json:"type"`
		CallID    *string         `json:"call_id"`
		Status    string          `json:"status,omitempty"`
		Execution string          `json:"execution"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.Arguments) == 0 {
		return fmt.Errorf("tool_search_call missing required 'arguments' field")
	}

	var arguments any
	if err := json.Unmarshal(aux.Arguments, &arguments); err != nil {
		return fmt.Errorf("tool_search_call arguments must be an object: %w", err)
	}
	object, ok := arguments.(map[string]any)
	if !ok {
		return fmt.Errorf("tool_search_call arguments must be an object")
	}

	c.ID = aux.ID
	c.Type = aux.Type
	c.CallID = aux.CallID
	c.Status = aux.Status
	c.Execution = aux.Execution
	c.Arguments = object
	return nil
}

func (ResponsesToolSearchCall) responsesInputItem() {}

// ResponsesToolSearchOutput supplies the tool declarations discovered by a
// preceding tool_search_call. The declarations become callable for this
// inference request.
type ResponsesToolSearchOutput struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"` // always "tool_search_output"
	CallID    *string         `json:"call_id"`
	Status    string          `json:"status"`
	Execution string          `json:"execution"`
	Tools     []ResponsesTool `json:"tools"`
}

func (ResponsesToolSearchOutput) responsesInputItem() {}

func appendAssistantToolCall(messages *[]api.Message, toolCall api.ToolCall, pendingThinking *string) {
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == "assistant" {
		lastMsg := &(*messages)[len(*messages)-1]
		lastMsg.ToolCalls = append(lastMsg.ToolCalls, toolCall)
		if *pendingThinking != "" {
			lastMsg.Thinking = *pendingThinking
			*pendingThinking = ""
		}
		return
	}

	msg := api.Message{Role: "assistant", ToolCalls: []api.ToolCall{toolCall}}
	if *pendingThinking != "" {
		msg.Thinking = *pendingThinking
		*pendingThinking = ""
	}
	*messages = append(*messages, msg)
}

func clientToolSearchCallID(execution string, callID *string, itemType string) (string, error) {
	if execution != "client" {
		return "", fmt.Errorf("%s execution must be %q or %q, got %q", itemType, "client", "server", execution)
	}
	if callID == nil || *callID == "" {
		return "", fmt.Errorf("%s with client execution requires a non-empty call_id", itemType)
	}
	return *callID, nil
}

func toolCallArguments(arguments map[string]any) (api.ToolCallFunctionArguments, error) {
	var converted api.ToolCallFunctionArguments
	b, err := json.Marshal(arguments)
	if err != nil {
		return converted, err
	}
	if err := json.Unmarshal(b, &converted); err != nil {
		return converted, err
	}
	return converted, nil
}

func appendConvertedTool(tools *[]api.Tool, indexes map[string]int, tool api.Tool) {
	if index, ok := indexes[tool.Function.Name]; ok {
		(*tools)[index] = tool
		return
	}
	indexes[tool.Function.Name] = len(*tools)
	*tools = append(*tools, tool)
}

func responsesRequestToolDeclarations(request ResponsesRequest) []ResponsesTool {
	tools := append([]ResponsesTool(nil), request.Tools...)
	for _, item := range request.Input.Items {
		switch item := item.(type) {
		case ResponsesAdditionalTools:
			tools = append(tools, item.Tools...)
		case ResponsesToolSearchOutput:
			tools = append(tools, item.Tools...)
		}
	}
	return tools
}

func toolSearchExecution(request ResponsesRequest, name string) (string, bool) {
	if name != "tool_search" {
		return "", false
	}
	for _, tool := range responsesRequestToolDeclarations(request) {
		if isToolSearchTool(tool) {
			execution := tool.Execution
			if execution == "" {
				execution = "client"
			}
			return execution, true
		}
	}
	return "", false
}

// responsesFunctionIdentity reverses the namespace flattening used by the
// internal chat API so Responses clients receive the namespace and member name
// as separate fields.
func responsesFunctionIdentity(request ResponsesRequest, qualifiedName string) (string, string) {
	for _, tool := range responsesRequestToolDeclarations(request) {
		if tool.Type != "namespace" || tool.Name == "" {
			continue
		}
		prefix := tool.Name + "."
		for _, member := range tool.Tools {
			memberName := member.Name
			if !strings.HasPrefix(memberName, prefix) {
				memberName = prefix + memberName
			}
			if memberName == qualifiedName {
				return tool.Name, strings.TrimPrefix(memberName, prefix)
			}
		}
	}
	return "", qualifiedName
}

// ResponsesToolCallOutputItems converts internal chat tool calls to their
// Responses output items while restoring deferred-search and namespace types.
func ResponsesToolCallOutputItems(responseID string, toolCalls []api.ToolCall, request ResponsesRequest) []ResponsesOutputItem {
	converted := ToToolCalls(toolCalls)
	output := make([]ResponsesOutputItem, 0, len(converted))
	for i, call := range converted {
		if execution, ok := toolSearchExecution(request, call.Function.Name); ok {
			var arguments map[string]any
			if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
				arguments = map[string]any{}
			}
			output = append(output, ResponsesOutputItem{
				ID:        fmt.Sprintf("tsc_%s_%d", responseID, i),
				Type:      "tool_search_call",
				Status:    "completed",
				CallID:    call.ID,
				Execution: execution,
				Arguments: arguments,
			})
			continue
		}

		namespace, name := responsesFunctionIdentity(request, call.Function.Name)
		output = append(output, ResponsesOutputItem{
			ID:        fmt.Sprintf("fc_%s_%d", responseID, i),
			Type:      "function_call",
			Status:    "completed",
			CallID:    call.ID,
			Namespace: namespace,
			Name:      name,
			Arguments: call.Function.Arguments,
		})
	}
	return output
}
