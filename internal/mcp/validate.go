// Minimal JSON-Schema subset validator for the MCP tool layer. Validates
// required fields, enum constraints, and basic JSON types. This is intentionally
// not a full JSON Schema engine — we only need to catch the obvious agent
// mistakes (missing `expression`, unknown enum value) before forwarding to the
// daemon. Issue #26.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validateArgs checks args (an arbitrary JSON object) against an objectSchema-
// produced map. Returns a single error joining all violations, or nil.
func validateArgs(schema map[string]interface{}, rawArgs json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var args map[string]interface{}
	if len(rawArgs) == 0 || string(rawArgs) == "null" {
		args = map[string]interface{}{}
	} else {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return fmt.Errorf("arguments must be a JSON object: %w", err)
		}
	}
	var errs []string

	// Required fields.
	if reqRaw, ok := schema["required"]; ok {
		if req, ok := reqRaw.([]string); ok {
			for _, name := range req {
				if _, present := args[name]; !present {
					errs = append(errs, fmt.Sprintf("missing required argument %q", name))
				}
			}
		}
	}

	// Per-property type + enum checks.
	if propsRaw, ok := schema["properties"]; ok {
		if props, ok := propsRaw.(map[string]interface{}); ok {
			for name, val := range args {
				propRaw, ok := props[name]
				if !ok {
					continue // additional properties allowed
				}
				prop, ok := propRaw.(map[string]interface{})
				if !ok {
					continue
				}
				if t, ok := prop["type"].(string); ok {
					if err := checkJSONType(name, t, val); err != "" {
						errs = append(errs, err)
					}
				}
				if enumRaw, ok := prop["enum"]; ok {
					if enum, ok := enumRaw.([]interface{}); ok {
						if !inEnum(val, enum) {
							errs = append(errs, fmt.Sprintf("argument %q value %v not in enum %v", name, val, enum))
						}
					} else if enum, ok := enumRaw.([]string); ok {
						if s, ok := val.(string); !ok || !containsStr(enum, s) {
							errs = append(errs, fmt.Sprintf("argument %q value %v not in enum %v", name, val, enum))
						}
					}
				}
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("inputSchema validation failed: %s", strings.Join(errs, "; "))
}

func checkJSONType(name, want string, v interface{}) string {
	switch want {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("argument %q must be string, got %T", name, v)
		}
	case "integer":
		// Accept any whole-number float too (JSON numbers come as float64).
		switch n := v.(type) {
		case float64:
			if n != float64(int64(n)) {
				return fmt.Sprintf("argument %q must be integer, got %v", name, v)
			}
		case int, int32, int64:
		default:
			return fmt.Sprintf("argument %q must be integer, got %T", name, v)
		}
	case "number":
		switch v.(type) {
		case float64, int, int32, int64:
		default:
			return fmt.Sprintf("argument %q must be number, got %T", name, v)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("argument %q must be boolean, got %T", name, v)
		}
	case "array":
		if _, ok := v.([]interface{}); !ok {
			return fmt.Sprintf("argument %q must be array, got %T", name, v)
		}
	case "object":
		if _, ok := v.(map[string]interface{}); !ok {
			return fmt.Sprintf("argument %q must be object, got %T", name, v)
		}
	}
	return ""
}

func inEnum(v interface{}, enum []interface{}) bool {
	for _, e := range enum {
		if fmt.Sprintf("%v", e) == fmt.Sprintf("%v", v) {
			return true
		}
	}
	return false
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
