package embedded

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const maxCPAAuthFileMetadataStringBytes = 16 * 1024

var cpaAuthFileControlFields = [...]string{"disabled", "note", "prefix", "websockets"}

// allowCPAAuthFileControlFields extends one provider's import-only schema with
// CPA instance controls. These values are validated and then discarded; they
// never become part of GPT-Load's canonical credential.
func allowCPAAuthFileControlFields(allowed map[string]struct{}) {
	for _, field := range cpaAuthFileControlFields {
		allowed[field] = struct{}{}
	}
}

func validateCPAAuthFileControlMetadata(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("credential must be one JSON object")
	}
	for _, field := range []string{"prefix", "note"} {
		value, present := fields[field]
		if !present {
			continue
		}
		var decoded string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("credential field %q is invalid", field)
		}
		if err := json.Unmarshal(value, &decoded); err != nil || len(decoded) > maxCPAAuthFileMetadataStringBytes {
			return fmt.Errorf("credential field %q is invalid", field)
		}
	}
	for _, field := range []string{"disabled", "websockets"} {
		value, present := fields[field]
		if !present {
			continue
		}
		var decoded bool
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("credential field %q is invalid", field)
		}
		if err := json.Unmarshal(value, &decoded); err != nil {
			return fmt.Errorf("credential field %q is invalid", field)
		}
	}
	return nil
}
