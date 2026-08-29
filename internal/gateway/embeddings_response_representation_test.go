package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"strconv"
	"testing"
	"unsafe"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
)

func TestEmbeddingsSuccessRepresentationPreservesOpaqueVectorsAndAliasesTopLevelModel(t *testing.T) {
	t.Parallel()

	body := []byte(`{"object":"list","model":"provider-model","data":[{"object":"embedding","index":0,"embedding":[0.12345678901234567,1]},{"object":"embedding","index":1,"embedding":"AAAA"}],"future":{"model":"provider-model","precise":1.2300}}`)
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		embeddingsForwardInput("public-model", "provider-model"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		body,
		[]string{"A", "1"},
	)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	for _, want := range [][]byte{
		[]byte(`"model":"public-model"`),
		[]byte(`"embedding":[0.12345678901234567,1]`),
		[]byte(`"embedding":"AAAA"`),
		[]byte(`"future":{"model":"provider-model","precise":1.2300}`),
	} {
		if !bytes.Contains(prepared.downstream, want) {
			t.Fatalf("downstream = %s, want %s", prepared.downstream, want)
		}
	}
}

func TestEmbeddingsSuccessRepresentationReusesAlreadyProjectedOpaqueBody(t *testing.T) {
	t.Parallel()

	body := []byte(`{"object":"list","model":"public-model","data":[{"embedding":[0.1]}]}`)
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		embeddingsForwardInput("public-model", "provider-model"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		body,
		nil,
	)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if !prepared.changed || len(prepared.downstream) == 0 ||
		unsafe.SliceData(prepared.downstream) != unsafe.SliceData(body) {
		t.Fatalf("prepared response copied or lost alias metadata: %#v", prepared)
	}
}

func TestEmbeddingsSuccessRepresentationFailsClosedOnCredentialOutsideVector(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{"object":"list","data":[{"embedding":[1]}],"vendor":"sk-secret"}`),
		[]byte(`{"object":"list","data":[{"embedding":[1]}],"embedding":"sk-secret"}`),
		[]byte(`{"object":"list","data":[{"embedding":[1]}],"vendor":{"embedding":"sk-secret"}}`),
	} {
		_, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
			embeddingsForwardInput("model", "model"),
			http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			body,
			[]string{"sk-secret"},
		)
		if err == nil {
			t.Fatalf("prepareSuccessRepresentation(%s) error = nil, want fail closed", body)
		}
	}
}

func TestEmbeddingsSuccessRepresentationFailsClosedOnDuplicateOpaqueKeys(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{"object":"list","data":[{"embedding":"sk-secret"}],"data":[{"embedding":[1]}]}`),
		[]byte(`{"object":"list","data":[{"embedding":"sk-secret","embedding":[1]}]}`),
	} {
		_, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
			embeddingsForwardInput("model", "model"),
			http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			body,
			[]string{"sk-secret"},
		)
		if err == nil {
			t.Fatalf("prepareSuccessRepresentation(%s) error = nil, want duplicate-key rejection", body)
		}
	}
}

func TestEmbeddingsSuccessRepresentationFailsClosedOnMalformedJSON(t *testing.T) {
	t.Parallel()

	_, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		embeddingsForwardInput("model", "model"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		[]byte(`{"object":"list","data":[{"embedding":[1]}]`),
		[]string{"sk-secret"},
	)
	if err == nil {
		t.Fatal("prepareSuccessRepresentation() error = nil, want malformed JSON rejection")
	}
}

func TestEmbeddingsSuccessRepresentationDecodesContentCodingBeforeOpaqueHandling(t *testing.T) {
	t.Parallel()

	body := []byte(`{"object":"list","model":"provider-model","data":[{"embedding":"AAAA"}]}`)
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		embeddingsForwardInput("public-model", "provider-model"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
		encoded.Bytes(),
		[]string{"A"},
	)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if prepared.headers.Get("Content-Encoding") != "" ||
		prepared.headers.Get("Content-Length") != strconv.Itoa(len(prepared.downstream)) ||
		!bytes.Contains(prepared.downstream, []byte(`"model":"public-model"`)) ||
		!bytes.Contains(prepared.downstream, []byte(`"embedding":"AAAA"`)) {
		t.Fatalf("prepared = headers=%v body=%s", prepared.headers, prepared.downstream)
	}
}

func TestEmbeddingsExecutionBoundaryMovesLargeBodyOwnership(t *testing.T) {
	t.Parallel()

	body := []byte(`{"object":"list","data":[{"embedding":[1]}]}`)
	result := execution.AttemptResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      http.StatusOK,
		Body:            body,
	}
	upstream := upstreamFromExecutionResult(
		context.Background(),
		ForwardInput{ClientProtocol: protocol.OpenAIEmbeddings},
		result,
	)
	if !bytes.Equal(upstream.Body, body) || unsafe.SliceData(upstream.Body) != unsafe.SliceData(body) {
		t.Fatal("upstreamFromExecutionResult copied the owned Embeddings body")
	}
}

func embeddingsForwardInput(externalModel, upstreamModel string) ForwardInput {
	return ForwardInput{
		Dialect:         dialect.NewOpenAIEmbeddings(),
		ClientProtocol:  protocol.OpenAIEmbeddings,
		Operation:       execution.OperationEmbeddingsCreate,
		ExternalModel:   externalModel,
		UpstreamModelID: upstreamModel,
	}
}
