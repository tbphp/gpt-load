package gateway

import (
	"bytes"
	"net/http"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
)

func TestRerankResponsePreservesDocumentTextAndRejectsCredentials(t *testing.T) {
	processor := &responseProcessor{redactor: redact.New()}
	input := ForwardInput{Dialect: dialect.NewRerank(), ClientProtocol: protocol.Rerank, ExternalModel: "m", UpstreamModelID: "m"}
	body := []byte(`{"results":[{"index":0,"relevance_score":0.1234567890123456789,"document":{"text":"sk-abcdefghijklmnopqrstuvwxyz012345678901234567890"}}],"future":1.2300}`)
	got, err := processor.prepareSuccessRepresentation(input, 200, http.Header{}, body, []string{"actual-secret"})
	if err != nil || !bytes.Equal(got.downstream, body) {
		t.Fatalf("response=%s error=%v", got.downstream, err)
	}
	for _, body := range []string{
		`{"results":[],"secret":"actual-secret"}`,
		`{"results":[],"secret":"actual-secret","secret":"safe"}`,
		`{"results":[{"document":{"text":"actual-secret"}}]}`,
		`{"results":`,
	} {
		if _, err := processor.prepareSuccessRepresentation(input, 200, http.Header{}, []byte(body), []string{"actual-secret"}); err == nil {
			t.Fatalf("unsafe response accepted: %s", body)
		}
	}
}
