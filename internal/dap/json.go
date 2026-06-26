package dap

import "encoding/json"

// marshal exists in its own file to avoid pulling encoding/json into client.go
// transitively where it would compete with godap types.
func marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
