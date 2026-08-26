package api

import (
	"bytes"
	"encoding/json"
	"sort"
)

func (p *ToolProperty) UnmarshalJSON(data []byte) error {
	type alias ToolProperty
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ToolProperty(decoded)

	return preserveUnknownSchemaFields(data, &p.AdditionalFields,
		"anyOf", "type", "items", "description", "enum", "properties", "required")
}

func (p ToolProperty) MarshalJSON() ([]byte, error) {
	type alias ToolProperty
	return marshalSchemaWithAdditionalFields(alias(p), p.AdditionalFields)
}

func (p *ToolFunctionParameters) UnmarshalJSON(data []byte) error {
	type alias ToolFunctionParameters
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = ToolFunctionParameters(decoded)

	return preserveUnknownSchemaFields(data, &p.AdditionalFields,
		"type", "$defs", "items", "required", "properties")
}

func (p ToolFunctionParameters) MarshalJSON() ([]byte, error) {
	type alias ToolFunctionParameters
	return marshalSchemaWithAdditionalFields(alias(p), p.AdditionalFields)
}

func preserveUnknownSchemaFields(data []byte, destination *map[string]json.RawMessage, known ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range known {
		delete(fields, name)
	}
	if len(fields) > 0 {
		*destination = fields
	}
	return nil
}

func marshalSchemaWithAdditionalFields(value any, additional map[string]json.RawMessage) ([]byte, error) {
	knownJSON, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(additional) == 0 {
		return knownJSON, nil
	}

	names := make([]string, 0, len(additional))
	for name := range additional {
		names = append(names, name)
	}
	sort.Strings(names)

	var encoded bytes.Buffer
	encoded.Write(knownJSON[:len(knownJSON)-1])
	needsComma := len(knownJSON) > 2
	for _, name := range names {
		if needsComma {
			encoded.WriteByte(',')
		}
		nameJSON, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		encoded.Write(nameJSON)
		encoded.WriteByte(':')
		encoded.Write(additional[name])
		needsComma = true
	}
	encoded.WriteByte('}')
	return encoded.Bytes(), nil
}
