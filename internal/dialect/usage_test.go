package dialect

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

var (
	_ Dialect = (*OpenAI)(nil)
	_ Dialect = (*Anthropic)(nil)
	_ Dialect = (*Gemini)(nil)
	_ Dialect = (*usageDialectOnly)(nil)
)

func TestUsageOptionalCapability(t *testing.T) {
	dialect := Dialect(&usageDialectOnly{})
	if dialect.Protocol() != protocol.OpenAICompletions {
		t.Fatalf("Protocol() = %q, want %q", dialect.Protocol(), protocol.OpenAICompletions)
	}
	if _, ok := dialect.(UsageExtractor); ok {
		t.Fatal("Dialect-only implementation unexpectedly has UsageExtractor capability")
	}
}

func TestUsageProviderNonStreamConformance(t *testing.T) {
	tests := []struct {
		name      string
		extractor UsageExtractor
		body      string
	}{
		{
			name:      "OpenAI",
			extractor: NewOpenAI(http.DefaultClient),
			body:      `{"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`,
		},
		{
			name:      "Anthropic",
			extractor: NewAnthropic(http.DefaultClient),
			body:      `{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"output_tokens":30}}`,
		},
		{
			name:      "Gemini",
			extractor: NewGemini(http.DefaultClient),
			body:      `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":30}}`,
		},
	}

	want := usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.extractor.ExtractUsage([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != usage.StateComplete || result.Tokens != want {
				t.Fatalf("ExtractUsage() = %#v, want complete with %#v", result, want)
			}
			requireUsageDiagnostics(t, result.Diagnostics)
			if delta, ok := result.Diagnostics.TotalDelta(); ok {
				t.Fatalf("TotalDelta() = %d, %t, want absent", delta, ok)
			}
		})
	}
}

func readUsageFixture(t *testing.T, provider, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "usage", provider, name))
	if err != nil {
		t.Fatalf("read %s usage fixture %s: %v", provider, name, err)
	}
	return body
}

func observeUsageJSONL(t *testing.T, stream UsageStreamExtractor, body []byte) {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for line := 1; scanner.Scan(); line++ {
		payload := bytes.TrimSpace(scanner.Bytes())
		if len(payload) == 0 {
			continue
		}
		if err := stream.Observe(payload); err != nil {
			t.Fatalf("Observe(line %d) error = %v", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan usage fixture: %v", err)
	}
}

func requireUsageDiagnostics(t *testing.T, diagnostics usage.Diagnostics, want ...usage.DiagnosticCode) {
	t.Helper()
	for _, code := range []usage.DiagnosticCode{
		usage.DiagnosticUnsupportedBillableDetail,
		usage.DiagnosticCacheWriteDefaulted5M,
		usage.DiagnosticNegativeValue,
		usage.DiagnosticInvalidNumber,
		usage.DiagnosticMissingRequiredField,
		usage.DiagnosticInconsistentTotal,
		usage.DiagnosticInvalidPayload,
		usage.DiagnosticInvalidEventSequence,
	} {
		wanted := false
		for _, expected := range want {
			if code == expected {
				wanted = true
				break
			}
		}
		if diagnostics.Has(code) != wanted {
			t.Fatalf("diagnostic %q present = %t, want %t", code, diagnostics.Has(code), wanted)
		}
	}
}

type usageDialectOnly struct{}

func (d *usageDialectOnly) Protocol() protocol.Protocol { return protocol.OpenAICompletions }

func (d *usageDialectOnly) InspectRequest(*ParsedRequest) (RequestMetadata, error) {
	return RequestMetadata{}, nil
}

func (d *usageDialectOnly) BuildUpstreamURL(string, *ParsedRequest) (string, error) { return "", nil }

func (d *usageDialectOnly) InjectCredential(http.Header, string) {}

func (d *usageDialectOnly) ListModels(context.Context, string, string, state.HeaderRules) ([]string, error) {
	return nil, nil
}

func (d *usageDialectOnly) Probe(context.Context, string, string, state.HeaderRules, string) error {
	return nil
}

func (d *usageDialectOnly) ClassifyStatus(int, []byte) health.FailureCategory {
	return health.FailureCategoryOK
}
