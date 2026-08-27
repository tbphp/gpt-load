package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIImagesCompatibleUsesCompletePrefixAndSanitizesGeneration(t *testing.T) {
	t.Parallel()

	const responseBody = `{"created":1,"data":[{"b64_json":"a2VlcC1tZQ==","url":"https://cdn.example/image.png?sig=sk-example-token"}],"unknown":{"keep":true}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tenant/api/v4/images/generations" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.URL.RawQuery != "vendor=one" {
			t.Errorf("raw query = %q", request.URL.RawQuery)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("X-Test-Header"); got != "keep" {
			t.Errorf("X-Test-Header = %q", got)
		}
		for _, name := range []string{"Api-Key", "X-Api-Key", "Proxy-Authorization"} {
			if got := request.Header.Get(name); got != "" {
				t.Errorf("client credential header %s reached upstream: %q", name, got)
			}
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("ReadAll(request.Body) error = %v", err)
			return
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil {
			t.Errorf("request body = %s: %v", body, err)
			return
		}
		if string(object["model"]) != `"provider-image"` {
			t.Errorf("model = %s", object["model"])
		}
		if raw, exists := object["stream"]; !exists || string(raw) != "false" {
			t.Errorf("stream = %s, exists=%t", raw, exists)
		}
		for _, field := range []string{"provider", "fallback", "fallbacks", "authorization", "api_key", "x-api-key"} {
			if _, exists := object[field]; exists {
				t.Errorf("control field %q reached upstream", field)
			}
		}
		if string(object["future_field"]) != `{"precise":1.2300}` {
			t.Errorf("future_field = %s", object["future_field"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIImagesSpec(
		channel.OpenAICompatible,
		server.URL+"/tenant/api/v4",
		execution.OperationImagesGenerate,
		"/v1/images/generations",
		"application/json",
		[]byte(`{"model":"public-image","stream":true,"prompt":"draw","provider":"client","fallback":"other","fallbacks":["other"],"authorization":"client","api_key":"client","x-api-key":"client","future_field":{"precise":1.2300}}`),
	)
	spec.RawQuery = "vendor=one&api_key=client-secret"
	spec.Query = nil
	spec = execution.NewAttemptSpec(spec)
	result := runtime.Execute(context.Background(), spec)

	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v; result=%+v", err, result)
	}
	if result.Error != nil || result.StatusCode != http.StatusOK || string(result.Body) != responseBody {
		t.Fatalf("result = %+v body=%s", result, result.Body)
	}
	if result.Usage != nil {
		t.Fatalf("Images usage = %#v, want nil", result.Usage)
	}
}

func TestNormalizeImagesResultsKeepOnlyObservedModel(t *testing.T) {
	spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIImages}

	missing := execution.AttemptResult{
		Model: "provider-image",
		Body:  []byte(`{"created":1,"data":[{"b64_json":"AA=="}]}`),
	}
	normalizeImagesAttemptResult(spec, &missing)
	if missing.Model != "" {
		t.Fatalf("missing response model = %q, want empty", missing.Model)
	}

	observed := execution.AttemptResult{
		Model: "provider-image",
		Body:  []byte(`{"model":"public-image","data":[{"b64_json":"AA=="}]}`),
	}
	normalizeImagesAttemptResult(spec, &observed)
	if observed.Model != "provider-image" {
		t.Fatalf("observed response model = %q, want provider-image", observed.Model)
	}

	stream := execution.StreamResult{Model: "provider-image"}
	normalizeImagesStreamResult(spec, &stream)
	if stream.Model != "" {
		t.Fatalf("unobserved stream model = %q, want empty", stream.Model)
	}
}

func TestOpenAIImagesJSONAddsMissingContentType(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"created":1,"data":[{"b64_json":"AA=="}]}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIImagesSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		execution.OperationImagesGenerate,
		"/v1/images/generations",
		"",
		[]byte(`{"model":"public-image","prompt":"draw"}`),
	)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
}

func TestOpenAIImagesUnaryAllowsBodyAboveDefaultLimit(t *testing.T) {
	prefix := []byte(`{"created":1,"data":[{"b64_json":"`)
	suffix := []byte(`"}]}`)
	body := make([]byte, 0, int(defaultMaxUnaryResponseBodyBytes)+1+len(prefix)+len(suffix))
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte{'A'}, int(defaultMaxUnaryResponseBodyBytes)+1)...)
	body = append(body, suffix...)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(body)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIImagesSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		execution.OperationImagesGenerate,
		"/v1/images/generations",
		"application/json",
		[]byte(`{"model":"public-image","prompt":"draw"}`),
	)
	spec.Timeouts = execution.AttemptTimeouts{
		FirstByte:  10 * time.Second,
		Request:    30 * time.Second,
		StreamIdle: 10 * time.Second,
	}
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
	if !bytes.Equal(result.Body, body) {
		t.Fatalf("result body length = %d, want %d", len(result.Body), len(body))
	}
}

func TestOpenAIImagesMultipartEditPreservesDataAndRemovesControlParts(t *testing.T) {
	t.Parallel()

	imageBytes := []byte{0x00, 0xff, 0x10, 0x20, '\r', '\n'}
	body, contentType := imagesMultipartBody(t, imageBytes)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %q", request.URL.Path)
		}
		mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
			t.Errorf("Content-Type = %q: %v", request.Header.Get("Content-Type"), err)
			return
		}
		reader := multipart.NewReader(request.Body, parameters["boundary"])
		var names []string
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("NextRawPart() error = %v", err)
				return
			}
			_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil {
				t.Errorf("Content-Disposition = %q: %v", part.Header.Get("Content-Disposition"), err)
				return
			}
			name := params["name"]
			names = append(names, name)
			data, err := io.ReadAll(part)
			if err != nil {
				t.Errorf("ReadAll(part) error = %v", err)
				return
			}
			switch name {
			case "model":
				if string(data) != "provider-image" {
					t.Errorf("model = %q", data)
				}
			case "stream":
				if string(data) != "false" {
					t.Errorf("stream = %q", data)
				}
			case "image[]":
				if !bytes.Equal(data, imageBytes) || part.Header.Get("X-Vendor-Part") != "keep" || params["filename"] != "../original.png" {
					t.Errorf("image part changed: data=%x header=%q filename=%q", data, part.Header.Get("X-Vendor-Part"), params["filename"])
				}
			case "api_key", "provider", "fallbacks":
				t.Errorf("control part %q reached upstream", name)
			}
		}
		if got := strings.Join(names, ","); got != "prompt,model,stream,image[],future" {
			t.Errorf("part order/names = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"created":1,"data":[{"b64_json":"b2s="}]}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIImagesSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		execution.OperationImagesEdit,
		"/v1/images/edits",
		contentType,
		body,
	)
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
}

func TestOpenAIImagesStreamForcesStreamAndForwardsSSEWithoutUsage(t *testing.T) {
	t.Parallel()

	const streamBody = "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cGFydA==\"}\n\n" +
		"event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"ZG9uZQ==\"}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %q", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		var object map[string]json.RawMessage
		if err := json.Unmarshal(body, &object); err != nil || string(object["stream"]) != "true" {
			t.Errorf("stream request body = %s, err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, streamBody)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	spec := openAIImagesSpec(
		channel.OpenAICompatible,
		server.URL+"/v1",
		execution.OperationImagesGenerate,
		"/v1/images/generations",
		"application/json",
		[]byte(`{"model":"public-image","stream":false,"prompt":"draw"}`),
	)
	var events []execution.StreamEvent
	result := runtime.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v, validation=%v", result, err)
	}
	var data bytes.Buffer
	for _, event := range events {
		if event.Kind == execution.StreamEventUsage {
			t.Fatalf("unexpected Images usage event: %+v", event)
		}
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
	}
	if data.String() != streamBody || result.Usage != nil {
		t.Fatalf("stream data/usage = %q/%#v", data.String(), result.Usage)
	}
}

func TestOpenAIImagesFailuresAreNotSentOrExplicitlyReplayUnknown(t *testing.T) {
	t.Parallel()

	t.Run("invalid body is not sent", func(t *testing.T) {
		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
		spec := openAIImagesSpec(
			channel.OpenAICompatible,
			"https://example.invalid/v1",
			execution.OperationImagesGenerate,
			"/v1/images/generations",
			"application/json",
			[]byte(`{"model":`),
		)
		result := runtime.Execute(context.Background(), spec)
		if result.DispatchState != execution.DispatchNotSent || result.Error == nil ||
			result.Error.Kind != execution.ErrorKindInvalidRequest ||
			result.Error.OriginHint != execution.ErrorOriginClient ||
			result.Error.ScopeHint != execution.ErrorScopeRequest ||
			result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("provider rejection is maybe sent and replay unknown", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, `{"error":{"type":"invalid_request_error","code":"unsupported_model","message":"unsupported"}}`)
		}))
		defer server.Close()
		runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
		spec := openAIImagesSpec(
			channel.OpenAICompatible,
			server.URL+"/v1",
			execution.OperationImagesGenerate,
			"/v1/images/generations",
			"application/json",
			[]byte(`{"model":"public-image","prompt":"draw"}`),
		)
		result := runtime.Execute(context.Background(), spec)
		if result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted || result.Error == nil ||
			result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestOpenAIImagesRequestShapeAndCapabilityAreExact(t *testing.T) {
	t.Parallel()

	manager := &RuntimeManager{}
	for _, providerKind := range []channel.ProviderKind{channel.ProviderOpenAI, channel.ProviderOpenAICompatible} {
		for _, operation := range []execution.Operation{execution.OperationImagesGenerate, execution.OperationImagesEdit} {
			route := channel.RouteDescriptor{
				ClientProtocol: protocol.OpenAIImages,
				Operation:      operation,
				RouteMode:      execution.RouteNative,
			}
			if err := manager.ValidateRouteCapability(providerKind, route); err != nil {
				t.Errorf("ValidateRouteCapability(%q, %q) error = %v", providerKind, operation, err)
			}
		}
	}
	for _, providerKind := range []channel.ProviderKind{channel.ProviderAnthropic, channel.ProviderXAI} {
		if err := manager.ValidateRouteCapability(providerKind, channel.RouteDescriptor{
			ClientProtocol: protocol.OpenAIImages,
			Operation:      execution.OperationImagesGenerate,
			RouteMode:      execution.RouteNative,
		}); err == nil {
			t.Errorf("provider %q unexpectedly supports Images", providerKind)
		}
	}

	valid := openAIImagesSpec(
		channel.OpenAICompatible,
		"https://example.com/v1",
		execution.OperationImagesGenerate,
		"/v1/images/generations",
		"application/json",
		[]byte(`{"model":"public-image"}`),
	)
	if !supportedRequestShape(valid, false) || !supportedRequestShape(valid, true) {
		t.Fatal("valid Images generation shape was rejected")
	}
	for _, mutate := range []func(*execution.AttemptSpec){
		func(spec *execution.AttemptSpec) { spec.Method = http.MethodGet },
		func(spec *execution.AttemptSpec) { spec.Path = "/v1/images/variations" },
		func(spec *execution.AttemptSpec) { spec.Operation = execution.OperationImagesEdit },
		func(spec *execution.AttemptSpec) { spec.RouteMode = execution.RouteConverted },
	} {
		invalid := valid.Clone()
		mutate(&invalid)
		if supportedRequestShape(invalid, false) {
			t.Errorf("invalid Images shape was accepted: %+v", invalid)
		}
	}
}

func openAIImagesSpec(
	channelID channel.ID,
	baseURL string,
	operation execution.Operation,
	path string,
	contentType string,
	body []byte,
) execution.AttemptSpec {
	return freezeTestAttempt(execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "images-request", AttemptID: "images-attempt", Sequence: 1,
		ChannelID: string(channelID), RouteMode: execution.RouteNative,
		ClientProtocol: protocol.OpenAIImages, Operation: operation,
		RouteRequirement: execution.RouteRequirementNative,
		ClientModel:      "public-image", UpstreamModel: "provider-image",
		Method: http.MethodPost, Path: path, Query: make(map[string][]string),
		Header: http.Header{
			"Content-Type":        {contentType},
			"Authorization":       {"Bearer client"},
			"Proxy-Authorization": {"Basic client"},
			"Api-Key":             {"client"},
			"X-Api-Key":           {"client"},
			"X-Test-Header":       {"keep"},
		},
		Body: body, TargetConfig: json.RawMessage(`{"base_url":"` + baseURL + `"}`),
		Timeouts:   execution.AttemptTimeouts{FirstByte: time.Second, Request: 2 * time.Second, StreamIdle: time.Second},
		Credential: execution.NewCredentialSnapshot(17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
	}))
}

func imagesMultipartBody(t *testing.T, imageBytes []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writeField := func(name, value string) {
		t.Helper()
		part, err := writer.CreateFormField(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(part, value)
	}
	writeField("prompt", "keep prompt")
	writeField("api_key", "client-secret")
	writeField("model", "public-image")
	writeField("provider", "client")
	writeField("stream", "true")
	writeField("fallbacks", "other")
	imageHeader := make(textproto.MIMEHeader)
	imageHeader.Set("Content-Disposition", `form-data; name="image[]"; filename="../original.png"`)
	imageHeader.Set("Content-Type", "image/png")
	imageHeader.Set("X-Vendor-Part", "keep")
	image, err := writer.CreatePart(imageHeader)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = image.Write(imageBytes)
	writeField("future", "keep")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(body.Bytes()), writer.FormDataContentType()
}
