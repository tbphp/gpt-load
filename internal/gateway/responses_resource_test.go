package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestResponsesResourcesRequireAccessKeyOwnership(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/responses/resp_private"},
		{http.MethodGet, "/v1/responses/resp_private?stream=true"},
		{http.MethodGet, "/v1/responses/resp_private/input_items?limit=20"},
		{http.MethodDelete, "/v1/responses/resp_private"},
		{http.MethodPost, "/v1/responses/resp_private/cancel"},
		{http.MethodHead, "/v1/responses/resp_private"},
		{http.MethodGet, "/v1/responses/resp_private/"},
		{http.MethodPatch, "/v1/responses/resp_private/vendor_action"},
		{http.MethodGet, "/v1/responses/%72esp_private/input_items"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			forwarder := &scriptedForwarder{
				results:       []UpstreamResult{storedResponse("resp_private"), storedResponse("resp_private")},
				streamResults: []UpstreamResult{{StatusCode: http.StatusOK, Committed: true}},
			}
			handler, engine, _ := newContinuationFixture(t, forwarder)
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
			other := handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-client")]
			other.ID = 2
			handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-other")] = other
			handler.manager.Current().AccessKeysByID[other.ID] = other
			for _, key := range []string{"gl-other", "gl-client"} {
				if key == "gl-client" {
					// 清空归属模拟未知、淘汰或未恢复的编号，不能回退到普通调度。
					handler.responseBindings = state.NewResponseBindings()
				}
				response := serveResponseResource(engine, key, test.method, test.path)
				if response.Code != http.StatusNotFound || len(forwarder.inputs) != 1 || len(forwarder.streamInputs) != 0 {
					t.Fatalf("key=%s status=%d upstream attempts=%d stream attempts=%d body=%s", key, response.Code, len(forwarder.inputs), len(forwarder.streamInputs), response.Body.String())
				}
				if strings.Contains(response.Body.String(), "resp_private") {
					t.Fatal("ownership rejection exposed the response ID")
				}
			}
		})
	}
}

func TestResponsesResourcesUseOwnersUpstreamCredential(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/responses/resp_private"},
		{http.MethodGet, "/v1/responses/resp_private/input_items?limit=20"},
		{http.MethodDelete, "/v1/responses/resp_private"},
		{http.MethodPost, "/v1/responses/resp_private/cancel"},
		{http.MethodHead, "/v1/responses/resp_private/vendor_state"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("resp_private"), storedResponse("resp_private")}}
			_, engine, _ := newContinuationFixture(t, forwarder)
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
			response := serveResponseResource(engine, "gl-client", test.method, test.path)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
		})
	}
}

func TestResponsesResourcesEnforceOwnershipBeforeHTTPForwarding(t *testing.T) {
	var resourceCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer sk-one" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost && request.URL.Path == "/v1/responses" {
			_, _ = writer.Write(storedResponse("resp_private").Body)
			return
		}
		resourceCalls.Add(1)
		payload := `{"id":"resp_private","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"private-canary"}]}]}`
		if request.URL.Query().Get("stream") == "true" {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(writer, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", payload)
			return
		}
		_, _ = io.WriteString(writer, payload)
	}))
	defer upstream.Close()
	handler, engine, _ := newContinuationFixture(t, newTestExecutionForwarder(t))
	setContinuationChannel(t, handler, channel.OpenAI, upstream.URL)
	other := handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-client")]
	other.ID = 2
	handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-other")] = other
	handler.manager.Current().AccessKeysByID[other.ID] = other
	if err := handler.registry.(*state.CredentialRegistry).SetCredentialStatus(2, state.CredentialStatusDisabled); err != nil {
		t.Fatal(err)
	}
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
	if _, found := handler.responseBindings.Lookup(1, "resp_private"); !found {
		t.Fatal("owner binding missing")
	}
	for _, query := range []string{"", "?stream=true"} {
		before := resourceCalls.Load()
		for _, key := range []string{"gl-other", "gl-client"} {
			response := serveResponseResource(engine, key, http.MethodGet, "/v1/responses/resp_private"+query)
			if key == "gl-other" {
				if response.Code != http.StatusNotFound || resourceCalls.Load() != before {
					t.Fatalf("cross-key retrieval: status=%d upstream calls=%d", response.Code, resourceCalls.Load())
				}
			} else if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "private-canary") || resourceCalls.Load() != before+1 {
				t.Fatalf("owner retrieval: status=%d upstream calls=%d body=%s", response.Code, resourceCalls.Load(), response.Body.String())
			}
		}
	}
}

func TestResponsesResourcesHonorCurrentAccessAndCredentialIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *Handler)
	}{
		{"disabled credential", func(t *testing.T, handler *Handler) {
			if err := handler.registry.(*state.CredentialRegistry).SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced credential", func(t *testing.T, handler *Handler) {
			ref, _ := handler.registry.CredentialRef(1)
			if err := handler.registry.(*state.CredentialRegistry).ApplyCredentialImport(1, []state.CredentialEntry{{
				ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: ref.Version + 1,
				IdentityGeneration: ref.IdentityGeneration + 1, Fingerprint: ref.Fingerprint, EncryptedValue: ref.EncryptedValue,
			}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"revoked group", func(_ *testing.T, handler *Handler) {
			key := handler.manager.Current().AccessKeysByID[1]
			key.Filters.Groups = map[uint]struct{}{2: {}}
			handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-client")] = key
			handler.manager.Current().AccessKeysByID[1] = key
		}},
		{"revoked protocol", func(_ *testing.T, handler *Handler) {
			key := handler.manager.Current().AccessKeysByID[1]
			key.Filters.Protocols = map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}}
			handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-client")] = key
			handler.manager.Current().AccessKeysByID[1] = key
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("resp_private"), storedResponse("must-not-forward")}}
			handler, engine, _ := newContinuationFixture(t, forwarder)
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
			test.change(t, handler)
			response := serveResponseResource(engine, "gl-client", http.MethodGet, "/v1/responses/resp_private")
			if response.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 1 {
				t.Fatalf("status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
			}
		})
	}
}

func TestResponsesResourcesDoNotSwitchAfterUpstreamFailure(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		storedResponse("resp_private"),
		{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"error":{"message":"unavailable"}}`)},
		storedResponse("must-not-forward"),
	}}
	handler, engine, _ := newContinuationFixture(t, forwarder)
	handler.manager.Current().Settings.RetryCount = 3
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
	response := serveResponseResource(engine, "gl-client", http.MethodGet, "/v1/responses/resp_private")
	if response.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
}

func TestResponsesResourcesRejectUnownedNamespaceExtensions(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("resp_private"), storedResponse("must-not-forward")}}
	_, engine, _ := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"private","store":true}`, http.StatusOK)
	response := serveResponseResource(engine, "gl-client", http.MethodPatch, "/v1/responses/vendor-extension/nested")
	if response.Code != http.StatusNotFound || len(forwarder.inputs) != 1 {
		t.Fatalf("status=%d attempts=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
}

func recordResponseOwnership(t *testing.T, handler *Handler, keyID, credentialID uint, responseID string) {
	t.Helper()
	ref, ok := handler.registry.CredentialRef(credentialID)
	if !ok || !handler.responseBindings.Record(keyID, responseID, ref) {
		t.Fatalf("record response %q ownership for key=%d credential=%d", responseID, keyID, credentialID)
	}
}

func serveResponseResource(engine http.Handler, key, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
