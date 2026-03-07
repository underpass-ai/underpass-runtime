package app

import (
	"encoding/json"
	"testing"
)

func TestValidateOutputAgainstSchema_AcceptsTypedArraySlices(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"command":{"type":"array"}}}`)
	output := map[string]any{
		"command": []string{"go", "test", "./..."},
	}

	if err := validateOutputAgainstSchema(schema, output); err != nil {
		t.Fatalf("expected typed slice array to be valid, got: %v", err)
	}
}

func TestValidateOutputAgainstSchema_RejectsWrongType(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"exit_code":{"type":"integer"}}}`)
	output := map[string]any{
		"exit_code": "0",
	}

	if err := validateOutputAgainstSchema(schema, output); err == nil {
		t.Fatal("expected validation error for wrong field type")
	}
}
