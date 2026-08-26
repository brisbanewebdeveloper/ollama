package api

import (
	"encoding/json"
	"testing"
)

func TestToolFunctionParametersPreserveUnknownJSONSchemaFields(t *testing.T) {
	input := []byte(`{
		"type":"object",
		"additionalProperties":false,
		"oneOf":[{"required":["prompt"]},{"required":["file_id"]}],
		"properties":{
			"prompt":{"type":"string","minLength":1,"pattern":"\\S"},
			"options":{"type":"object","additionalProperties":{"type":"string"}}
		},
		"required":["prompt"]
	}`)

	var parameters ToolFunctionParameters
	if err := json.Unmarshal(input, &parameters); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}

	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatal(err)
	}
	if !jsonValuesEqual(got, want) {
		t.Fatalf("schema changed during round trip\ngot:  %s\nwant: %s", encoded, input)
	}
}

func jsonValuesEqual(a, b any) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}
