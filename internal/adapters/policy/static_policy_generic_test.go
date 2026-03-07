package policy

import (
	"testing"
)

const (
	testFieldItems          = "items"
	testReasonItemDenied    = "item not allowed"
	testUnexpectedReasonFmt = "unexpected reason: %q"
)

// ---------------------------------------------------------------------------
// extractFieldValues
// ---------------------------------------------------------------------------

func TestExtractFieldValues_EmptyFieldName(t *testing.T) {
	values, err := extractFieldValues(map[string]any{"x": "v"}, "", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if values != nil {
		t.Fatalf("expected nil for empty field name, got %#v", values)
	}
}

func TestExtractFieldValues_WhitespaceFieldName(t *testing.T) {
	values, err := extractFieldValues(map[string]any{"x": "v"}, "   ", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if values != nil {
		t.Fatalf("expected nil for whitespace field name, got %#v", values)
	}
}

func TestExtractFieldValues_FieldNotFound(t *testing.T) {
	values, err := extractFieldValues(map[string]any{"a": "v"}, "missing", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if values != nil {
		t.Fatalf("expected nil for missing field, got %#v", values)
	}
}

func TestExtractFieldValues_SingleString(t *testing.T) {
	values, err := extractFieldValues(map[string]any{"name": "hello"}, "name", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if len(values) != 1 || values[0] != "hello" {
		t.Fatalf("expected [hello], got %#v", values)
	}
}

func TestExtractFieldValues_SingleNonString(t *testing.T) {
	_, err := extractFieldValues(map[string]any{"num": 42}, "num", false)
	if err == nil {
		t.Fatal("expected error for non-string single field")
	}
}

func TestExtractFieldValues_MultiStringArray(t *testing.T) {
	payload := map[string]any{"tags": []any{"a", "b", "c"}}
	values, err := extractFieldValues(payload, "tags", true)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if len(values) != 3 || values[0] != "a" || values[1] != "b" || values[2] != "c" {
		t.Fatalf("expected [a b c], got %#v", values)
	}
}

func TestExtractFieldValues_MultiNonArray(t *testing.T) {
	payload := map[string]any{"tags": "not-an-array"}
	_, err := extractFieldValues(payload, "tags", true)
	if err == nil {
		t.Fatal("expected error when multi field is not an array")
	}
}

func TestExtractFieldValues_MultiArrayWithNonString(t *testing.T) {
	payload := map[string]any{"tags": []any{"valid", 123}}
	_, err := extractFieldValues(payload, "tags", true)
	if err == nil {
		t.Fatal("expected error when multi array contains non-string")
	}
}

func TestExtractFieldValues_NestedDotPath(t *testing.T) {
	payload := map[string]any{
		"config": map[string]any{
			"name": "nested-value",
		},
	}
	values, err := extractFieldValues(payload, "config.name", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if len(values) != 1 || values[0] != "nested-value" {
		t.Fatalf("expected [nested-value], got %#v", values)
	}
}

func TestExtractFieldValues_NestedDotPathNotFound(t *testing.T) {
	payload := map[string]any{"config": map[string]any{"name": "ok"}}
	values, err := extractFieldValues(payload, "config.missing", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if values != nil {
		t.Fatalf("expected nil for missing nested path, got %#v", values)
	}
}

func TestExtractFieldValues_NonObjectPayload(t *testing.T) {
	values, err := extractFieldValues("just-a-string", "field", false)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if values != nil {
		t.Fatalf("expected nil for non-object payload, got %#v", values)
	}
}

func TestExtractFieldValues_EmptyMultiArray(t *testing.T) {
	payload := map[string]any{testFieldItems: []any{}}
	values, err := extractFieldValues(payload, testFieldItems, true)
	if err != nil {
		t.Fatalf(testUnexpectedErrorFmt, err)
	}
	if len(values) != 0 {
		t.Fatalf("expected empty slice, got %#v", values)
	}
}

// ---------------------------------------------------------------------------
// checkFieldValuesAllowed
// ---------------------------------------------------------------------------

func TestCheckFieldValuesAllowed_AllAllowed(t *testing.T) {
	payload := map[string]any{testFieldItems: []any{"apple", "banana"}}
	allowed, reason := checkFieldValuesAllowed(payload, testFieldItems, true,
		func(v string) bool { return v == "apple" || v == "banana" },
		testReasonItemDenied)
	if !allowed || reason != "" {
		t.Fatalf("expected all allowed, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestCheckFieldValuesAllowed_OneDenied(t *testing.T) {
	payload := map[string]any{testFieldItems: []any{"apple", "poison"}}
	allowed, reason := checkFieldValuesAllowed(payload, testFieldItems, true,
		func(v string) bool { return v == "apple" },
		testReasonItemDenied)
	if allowed {
		t.Fatal("expected denial for non-matching value")
	}
	if reason != testReasonItemDenied {
		t.Fatalf(testUnexpectedReasonFmt, reason)
	}
}

func TestCheckFieldValuesAllowed_ExtractionError(t *testing.T) {
	// Multi field but value is not an array → extraction error
	payload := map[string]any{testFieldItems: "not-array"}
	allowed, reason := checkFieldValuesAllowed(payload, testFieldItems, true,
		func(v string) bool { return true },
		testReasonItemDenied)
	if allowed {
		t.Fatal("expected denial on extraction error")
	}
	if reason != "invalid field payload" {
		t.Fatalf(testUnexpectedReasonFmt, reason)
	}
}

func TestCheckFieldValuesAllowed_EmptyValuesSkipped(t *testing.T) {
	payload := map[string]any{testFieldItems: []any{"  ", ""}}
	allowed, reason := checkFieldValuesAllowed(payload, testFieldItems, true,
		func(v string) bool { return false }, // would deny any non-empty value
		testReasonItemDenied)
	if !allowed || reason != "" {
		t.Fatalf("expected empty/whitespace values to be skipped, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestCheckFieldValuesAllowed_FieldNotFound(t *testing.T) {
	payload := map[string]any{"other": "value"}
	allowed, reason := checkFieldValuesAllowed(payload, "missing", false,
		func(v string) bool { return false },
		"denied")
	if !allowed || reason != "" {
		t.Fatalf("expected allowed for missing field, got allowed=%v reason=%q", allowed, reason)
	}
}

func TestCheckFieldValuesAllowed_SingleValue(t *testing.T) {
	payload := map[string]any{"name": "ok"}
	allowed, reason := checkFieldValuesAllowed(payload, "name", false,
		func(v string) bool { return v == "ok" },
		"denied")
	if !allowed || reason != "" {
		t.Fatalf("expected allowed, got allowed=%v reason=%q", allowed, reason)
	}

	allowed, reason = checkFieldValuesAllowed(payload, "name", false,
		func(v string) bool { return v == "other" },
		"not matching")
	if allowed {
		t.Fatal("expected denial for non-matching single value")
	}
	if reason != "not matching" {
		t.Fatalf(testUnexpectedReasonFmt, reason)
	}
}
