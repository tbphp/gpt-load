package httpheader

import "testing"

// This contract keeps representation metadata under gateway control.
func TestContentCodingHeadersAreReservedForUpstreamRequests(t *testing.T) {
	for _, name := range []string{
		"Accept-Encoding",
		"content-encoding",
		"CONTENT-LENGTH",
	} {
		if !IsForbiddenRequestRuleSetName(name) {
			t.Errorf("IsForbiddenRequestRuleSetName(%q) = false, want true", name)
		}
	}
}
