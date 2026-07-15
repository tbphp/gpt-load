package utils

import (
	"net/http"
	"testing"
)

func TestResolveHeaderVariables(t *testing.T) {
	ctx := &HeaderVariableContext{
		ClientIP:  "192.0.2.10",
		GroupName: "primary",
		APIKey:    "sk-test",
	}

	got := ResolveHeaderVariables("${CLIENT_IP}|${GROUP_NAME}|${API_KEY}", ctx)
	want := "192.0.2.10|primary|sk-test"
	if got != want {
		t.Fatalf("ResolveHeaderVariables() = %q, want %q", got, want)
	}
}

func TestApplyHeaderRules(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Remove-Me", "old")

	ApplyHeaderRules(req, []HeaderRule{
		{Action: "remove", Key: "X-Remove-Me"},
		{Action: "set", Key: "X-Group", Value: "${GROUP_NAME}"},
	}, &HeaderVariableContext{GroupName: "primary"})

	if got := req.Header.Get("X-Remove-Me"); got != "" {
		t.Errorf("removed header = %q, want empty", got)
	}
	if got := req.Header.Get("X-Group"); got != "primary" {
		t.Errorf("set header = %q, want %q", got, "primary")
	}
}

func TestApplyHeaderRulesHandlesNilRequest(t *testing.T) {
	ApplyHeaderRules(nil, []HeaderRule{{Action: "set", Key: "X-Test", Value: "value"}}, nil)
}
