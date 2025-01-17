package vervet

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ghodss/yaml"
)

// ToSpecJSON renders an OpenAPI document object as JSON.
func ToSpecJSON(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// ToSpecYAML renders an OpenAPI document object as YAML.
func ToSpecYAML(v interface{}) ([]byte, error) {
	jsonBuf, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}
	yamlBuf, err := yaml.JSONToYAML(jsonBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal YAML: %w", err)
	}
	return WithGeneratedComment(yamlBuf)
}

// WithGeneratedComment prepends a comment to YAML output indicating the file
// was generated.
func WithGeneratedComment(yamlBuf []byte) ([]byte, error) {
	var buf bytes.Buffer
	_, err := fmt.Fprintf(&buf, "# OpenAPI spec generated by vervet, DO NOT EDIT\n")
	if err != nil {
		return nil, fmt.Errorf("failed to write output: %w", err)
	}
	_, err = buf.Write(yamlBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to write output: %w", err)
	}
	return buf.Bytes(), nil
}
