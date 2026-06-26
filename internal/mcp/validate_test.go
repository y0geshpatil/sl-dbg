package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateArgsRequired(t *testing.T) {
	schema := objectSchema([]string{"expression"}, map[string]interface{}{
		"expression": stringProp("expr"),
		"frame":      intProp("frame"),
	})
	if err := validateArgs(schema, json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected missing-required error")
	} else if !strings.Contains(err.Error(), "expression") {
		t.Errorf("err should mention missing field: %v", err)
	}
	if err := validateArgs(schema, json.RawMessage(`{"expression":"x"}`)); err != nil {
		t.Errorf("present required must pass: %v", err)
	}
}

func TestValidateArgsType(t *testing.T) {
	schema := objectSchema(nil, map[string]interface{}{
		"frame": intProp("frame"),
	})
	if err := validateArgs(schema, json.RawMessage(`{"frame":"top"}`)); err == nil {
		t.Fatal("expected wrong-type error")
	}
	if err := validateArgs(schema, json.RawMessage(`{"frame":0}`)); err != nil {
		t.Errorf("integer arg must pass: %v", err)
	}
	if err := validateArgs(schema, json.RawMessage(`{"frame":1.5}`)); err == nil {
		t.Errorf("non-integer number should fail integer check")
	}
}

func TestValidateArgsNullAndEmpty(t *testing.T) {
	schema := objectSchema(nil, map[string]interface{}{
		"frame": intProp("frame"),
	})
	if err := validateArgs(schema, nil); err != nil {
		t.Errorf("nil args must pass when no required: %v", err)
	}
	if err := validateArgs(schema, json.RawMessage(`null`)); err != nil {
		t.Errorf("null args must pass: %v", err)
	}
}

func TestValidateArgsAdditionalAllowed(t *testing.T) {
	schema := objectSchema(nil, map[string]interface{}{
		"frame": intProp("frame"),
	})
	// session is added automatically by objectSchema; extra "weird" arg must
	// not fail validation (we are permissive on unknown fields).
	err := validateArgs(schema, json.RawMessage(`{"frame":0,"weird":"x"}`))
	if err != nil {
		t.Errorf("unknown arg must be tolerated: %v", err)
	}
}
