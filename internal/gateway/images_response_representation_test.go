package gateway

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
)

func TestImagesSuccessRepresentationPreservesSignedURL(t *testing.T) {
	t.Parallel()

	body := []byte(`{"created":1,"data":[{"url":"https://cdn.example/image.png?signature=abc123&expires=999"}]}`)
	headers := http.Header{
		"Content-Type":   {"application/json"},
		"Content-Length": {strconv.Itoa(len(body))},
	}
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		ForwardInput{
			Dialect:         dialect.NewOpenAIResponses(),
			ClientProtocol:  protocol.Protocol("openai-images"),
			Operation:       execution.Operation("images_generate"),
			ExternalModel:   "gpt-image-2",
			UpstreamModelID: "gpt-image-2",
		},
		http.StatusOK,
		headers,
		body,
		nil,
	)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if !bytes.Equal(prepared.downstream, body) {
		t.Fatalf("downstream body = %s, want exact %s", prepared.downstream, body)
	}
}

func TestImagesSuccessRepresentationFailsClosedOnKnownCredential(t *testing.T) {
	t.Parallel()

	const secret = "sk-image-upstream-secret"
	body := []byte(`{"created":1,"data":[{"revised_prompt":"` + secret + `"}]}`)
	_, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		ForwardInput{
			Dialect:         dialect.NewOpenAIResponses(),
			ClientProtocol:  protocol.Protocol("openai-images"),
			Operation:       execution.Operation("images_generate"),
			ExternalModel:   "gpt-image-2",
			UpstreamModelID: "gpt-image-2",
		},
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		body,
		[]string{secret},
	)
	if err == nil {
		t.Fatal("prepareSuccessRepresentation() error = nil, want fail closed")
	}
}
