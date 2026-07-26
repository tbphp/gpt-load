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
)

var (
	_ Dialect = (*OpenAI)(nil)
	_ Dialect = (*Anthropic)(nil)
	_ Dialect = (*Gemini)(nil)
	_ Dialect = (*usageDialectOnly)(nil)
)

func TestUsageOptionalCapability(t *testing.T) {
	dialect := Dialect(&usageDialectOnly{})
	if dialect.Protocol() != protocol.OpenAI {
		t.Fatalf("Protocol() = %q, want %q", dialect.Protocol(), protocol.OpenAI)
	}
	if _, ok := dialect.(UsageExtractor); ok {
		t.Fatal("Dialect-only implementation unexpectedly has UsageExtractor capability")
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

type usageDialectOnly struct{}

func (d *usageDialectOnly) Protocol() protocol.Protocol { return protocol.OpenAI }

func (d *usageDialectOnly) ExtractModel(*ParsedRequest) (string, bool, error) { return "", false, nil }

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
