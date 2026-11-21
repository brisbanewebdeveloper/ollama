package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

var toolPropertyKeys = []string{"anyOf", "type", "items", "description", "enum", "properties", "required"}

func (tp *ToolProperty) UnmarshalJSON(data []byte) error {
	type plain ToolProperty
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	extra, err := unmodeledSchemaKeys(data, toolPropertyKeys)
	if err != nil {
		return err
	}
	p.Extra = extra
	*tp = ToolProperty(p)
	return nil
}

func (tp ToolProperty) MarshalJSON() ([]byte, error) {
	type plain ToolProperty
	return marshalWithExtra(plain(tp), tp.Extra, toolPropertyKeys)
}

var toolFunctionParametersKeys = []string{"type", "$defs", "items", "required", "properties"}

func (t *ToolFunctionParameters) UnmarshalJSON(data []byte) error {
	type plain ToolFunctionParameters
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	extra, err := unmodeledSchemaKeys(data, toolFunctionParametersKeys)
	if err != nil {
		return err
	}
	p.Extra = extra
	*t = ToolFunctionParameters(p)
	return nil
}

func (t ToolFunctionParameters) MarshalJSON() ([]byte, error) {
	type plain ToolFunctionParameters
	return marshalWithExtra(plain(t), t.Extra, toolFunctionParametersKeys)
}

// TODO(jberg5): once go.mod is on Go 1.27, replace isModeledKey,
// unmodeledSchemaKeys and marshalWithExtra with an encoding/json/v2
// `json:",embed"` jsontext.Value field, which also keeps the client's key order.
func isModeledKey(key string, known []string) bool {
	return slices.ContainsFunc(known, func(k string) bool { return strings.EqualFold(k, key) })
}

func unmodeledSchemaKeys(data []byte, known []string) (map[string]any, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var extra map[string]any
	for k, v := range raw {
		if isModeledKey(k, known) {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(v))
		dec.UseNumber()
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		if extra == nil {
			extra = make(map[string]any)
		}
		extra[k] = val
	}
	return extra, nil
}

func marshalWithExtra(v any, extra map[string]any, known []string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	if len(extra) == 0 {
		return out, nil
	}
	if len(out) < 2 || out[len(out)-1] != '}' {
		return nil, fmt.Errorf("marshalWithExtra: %T did not marshal to a JSON object", v)
	}
	res := bytes.NewBuffer(append([]byte(nil), out[:len(out)-1]...))
	empty := len(out) == 2
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		if isModeledKey(k, known) {
			continue
		}
		var vb bytes.Buffer
		ve := json.NewEncoder(&vb)
		ve.SetEscapeHTML(false)
		if err := ve.Encode(extra[k]); err != nil {
			return nil, fmt.Errorf("marshal schema keyword %q: %w", k, err)
		}
		kb, _ := json.Marshal(k)
		if !empty {
			res.WriteByte(',')
		}
		empty = false
		res.Write(kb)
		res.WriteByte(':')
		res.Write(bytes.TrimRight(vb.Bytes(), "\n"))
	}
	res.WriteByte('}')
	return res.Bytes(), nil
}
