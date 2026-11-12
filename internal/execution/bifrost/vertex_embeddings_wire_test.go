package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	core "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/providers/vertex"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestVertexEmbeddingsSDKPreservesContent(t *testing.T) {
	t.Parallel()

	const content = `{"parts":[{"text":"models/example"},{"text":"projects/other/locations/elsewhere/publishers/google/models/example"},{"text":"/locations/final"},{"text":"models\/escaped"},{"text":"backslash\\/locations/elsewhere/end"},{"fileData":{"fileUri":"gs://bucket/projects/other/locations/elsewhere/image.png","mimeType":"image/png"}}]}`
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		action   string
		body     string
		wantBody string
	}{
		{
			name: "native single", protocol: protocol.GeminiEmbeddings, action: "embedContent",
			body:     `{"model":"models/public","content":` + content + `,"extension":{"model":"models/business"}}`,
			wantBody: `{"model":"","content":` + content + `,"extension":{"model":"models/business"}}`,
		},
		{
			name: "native batch", protocol: protocol.GeminiEmbeddings, action: "batchEmbedContents",
			body:     `{"requests":[{"model":"models/public","content":` + content + `}]}`,
			wantBody: `{"requests":[{"model":"","content":` + content + `}]}`,
		},
		{
			name: "converted single", protocol: protocol.OpenAIEmbeddings, action: "batchEmbedContents",
			body:     `{"model":"public-embedding","input":"models/example","dimensions":256}`,
			wantBody: `{"requests":[{"model":"","content":{"parts":[{"text":"models/example"}]},"outputDimensionality":256}]}`,
		},
		{
			name: "converted batch", protocol: protocol.OpenAIEmbeddings, action: "batchEmbedContents",
			body:     `{"model":"public-embedding","input":["models/example","projects/other/locations/elsewhere/publishers/google/models/example","/locations/final","models\/escaped","backslash\\/locations/elsewhere/end"]}`,
			wantBody: `{"requests":[{"model":"","content":{"parts":[{"text":"models/example"}]}},{"model":"","content":{"parts":[{"text":"projects/other/locations/elsewhere/publishers/google/models/example"}]}},{"model":"","content":{"parts":[{"text":"/locations/final"}]}},{"model":"","content":{"parts":[{"text":"models/escaped"}]}},{"model":"","content":{"parts":[{"text":"backslash\\/locations/elsewhere/end"}]}}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			captured := make(chan []byte, 1)
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				wantPath := "/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-embedding-2:" + test.action
				if request.Method != http.MethodPost || request.URL.Path != wantPath || request.Header.Get("Content-Type") != "application/json" {
					t.Errorf("upstream request = %s %s, content type %q", request.Method, request.URL.Path, request.Header.Get("Content-Type"))
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read upstream body: %v", err)
				}
				captured <- body
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Connection", "close")
				_, _ = io.WriteString(writer, `{"embeddings":[{"values":[0.1]}]}`)
			}))
			defer upstream.Close()
			proxy := newVertexEmbeddingsTestProxy(t, upstream.Listener.Addr().String())
			defer proxy.Close()

			runtime := newTestRuntime(t)
			spec := vertexEmbeddingsSpec(test.protocol, execution.OperationEmbeddingsCreate, test.action, test.body)
			prepared, failure := runtime.prepare(spec, false)
			if failure != nil {
				t.Fatalf("prepare failed: %+v", failure.Error)
			}
			provider, err := vertex.NewVertexProvider(&schemas.ProviderConfig{
				NetworkConfig: schemas.NetworkConfig{
					AllowPrivateNetwork: true, InsecureSkipVerify: true,
					DefaultRequestTimeoutInSeconds: 5, StreamIdleTimeoutInSeconds: 5,
				},
				ProxyConfig: &schemas.ProxyConfig{Type: schemas.HTTPProxy, URL: plainSecretPtr(proxy.URL)},
			}, core.NewNoOpLogger())
			if err != nil {
				t.Fatal(err)
			}
			// 用合成 API key 避免真实 OAuth 调用；正文和 URL 仍经过实际 Vertex SDK 发送路径。
			key := prepared.directKey
			key.Value = plainSecret("synthetic-api-key")
			ctx := schemas.NewBifrostContext(t.Context(), time.Now().Add(5*time.Second))
			defer ctx.Cancel()
			stream, sdkError := provider.PassthroughStream(ctx,
				func(_ *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError) {
					return result, err
				}, func(context.Context) {}, key, prepared.passthrough)
			if sdkError != nil {
				t.Fatalf("SDK passthrough failed: %+v", sdkError)
			}
			for chunk := range stream {
				if chunk.BifrostError != nil {
					t.Errorf("SDK stream error: %+v", chunk.BifrostError)
				}
			}
			var body []byte
			select {
			case body = <-captured:
			case <-ctx.Done():
				t.Fatal("SDK did not send the request to the local upstream")
			}
			var got, want any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("upstream JSON was corrupted: %v; body = %s", err, body)
			}
			if err := json.Unmarshal([]byte(test.wantBody), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("upstream body = %s, want %s", body, test.wantBody)
			}
		})
	}
}

// 将固定 Vertex 主机的 CONNECT 隧道转到本地 TLS 服务，避免测试依赖外网。
func newVertexEmbeddingsTestProxy(t *testing.T, upstreamAddress string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect || request.Host != "us-central1-aiplatform.googleapis.com:443" {
			t.Errorf("unexpected proxy target: %s %s", request.Method, request.Host)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		upstream, err := net.DialTimeout("tcp", upstreamAddress, time.Second)
		if err != nil {
			t.Errorf("connect local upstream: %v", err)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, buffered, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		defer client.Close()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			t.Errorf("write CONNECT response: %v", err)
			return
		}
		if err := buffered.Flush(); err != nil {
			t.Errorf("flush CONNECT response: %v", err)
			return
		}
		copied := make(chan struct{})
		go func() {
			defer close(copied)
			// 连接随请求结束关闭，复制错误由上层 SDK 请求结果报告。
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-copied
	}))
}
