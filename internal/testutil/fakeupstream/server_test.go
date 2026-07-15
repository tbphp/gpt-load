package fakeupstream

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dialectTestCase struct {
	name              string
	jsonPath          string
	streamPath        string
	streamUsageMarker string
	resetHeader       string
}

var dialectTestCases = []dialectTestCase{
	{
		name:              "openai",
		jsonPath:          "/v1/chat/completions",
		streamPath:        "/v1/chat/completions",
		streamUsageMarker: `"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}`,
		resetHeader:       "X-RateLimit-Reset-Requests",
	},
	{
		name:              "anthropic",
		jsonPath:          "/v1/messages",
		streamPath:        "/v1/messages",
		streamUsageMarker: `"usage":{"output_tokens":2}`,
		resetHeader:       "Anthropic-RateLimit-Requests-Reset",
	},
	{
		name:              "gemini",
		jsonPath:          "/v1beta/models/gemini-2.0-flash:generateContent",
		streamPath:        "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
		streamUsageMarker: `"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}`,
		resetHeader:       "X-RateLimit-Reset",
	},
}

func TestServerServesSuccessFixturesAndRecordsRequests(t *testing.T) {
	for _, tc := range dialectTestCases {
		t.Run(tc.name, func(t *testing.T) {
			server := New(Step{
				Status:  http.StatusOK,
				Fixture: filepath.Join(tc.name, "success.json"),
			})
			defer server.Close()

			requestBody := []byte(`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`)
			req, err := http.NewRequest(http.MethodPost, server.URL+tc.jsonPath, bytes.NewReader(requestBody))
			if err != nil {
				t.Fatalf("创建请求失败: %v", err)
			}
			req.Header.Set("Authorization", "Bearer secret-token")
			req.Header.Set("X-Test-Request", tc.name)

			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("请求 fake upstream 失败: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("响应状态 = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			assertFixtureBody(t, body, tc.name, "success.json")

			requests := server.Requests()
			if len(requests) != 1 {
				t.Fatalf("记录请求数 = %d, want 1", len(requests))
			}
			recorded := requests[0]
			if recorded.Method != http.MethodPost {
				t.Errorf("method = %q, want POST", recorded.Method)
			}
			if recorded.Path != strings.Split(tc.jsonPath, "?")[0] {
				t.Errorf("path = %q, want %q", recorded.Path, tc.jsonPath)
			}
			if recorded.Headers.Get("Authorization") != "Bearer secret-token" {
				t.Errorf("Authorization header 未被记录")
			}
			if !bytes.Equal(recorded.Body, requestBody) {
				t.Errorf("body = %s, want %s", recorded.Body, requestBody)
			}

			// Requests 必须返回快照，避免测试调用方意外篡改服务器内部记录。
			requests[0].Headers.Set("Authorization", "changed")
			requests[0].Body[0] = 'x'
			fresh := server.Requests()[0]
			if fresh.Headers.Get("Authorization") != "Bearer secret-token" {
				t.Errorf("修改请求快照污染了内部 header 记录")
			}
			if !bytes.Equal(fresh.Body, requestBody) {
				t.Errorf("修改请求快照污染了内部 body 记录")
			}
		})
	}
}

func TestServerServesScriptedErrorsInOrder(t *testing.T) {
	for _, tc := range dialectTestCases {
		t.Run(tc.name, func(t *testing.T) {
			server := New(
				Step{Status: http.StatusUnauthorized, Fixture: filepath.Join(tc.name, "401.json")},
				Step{
					Status:  http.StatusTooManyRequests,
					Fixture: filepath.Join(tc.name, "429.json"),
					Headers: http.Header{
						"Retry-After":  {"2"},
						tc.resetHeader: {"1"},
					},
				},
				Step{Status: http.StatusInternalServerError, Fixture: filepath.Join(tc.name, "500.json")},
			)
			defer server.Close()

			wantStatuses := []int{
				http.StatusUnauthorized,
				http.StatusTooManyRequests,
				http.StatusInternalServerError,
			}
			fixtureNames := []string{"401.json", "429.json", "500.json"}
			for i, wantStatus := range wantStatuses {
				resp, err := server.Client().Post(server.URL+tc.jsonPath, "application/json", strings.NewReader(`{}`))
				if err != nil {
					t.Fatalf("第 %d 个请求失败: %v", i+1, err)
				}
				body, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if readErr != nil {
					t.Fatalf("读取第 %d 个响应失败: %v", i+1, readErr)
				}
				if resp.StatusCode != wantStatus {
					t.Errorf("第 %d 个响应状态 = %d, want %d", i+1, resp.StatusCode, wantStatus)
				}
				assertFixtureBody(t, body, tc.name, fixtureNames[i])
				if wantStatus == http.StatusTooManyRequests {
					if resp.Header.Get("Retry-After") != "2" {
						t.Errorf("Retry-After = %q, want 2", resp.Header.Get("Retry-After"))
					}
					if resp.Header.Get(tc.resetHeader) != "1" {
						t.Errorf("%s = %q, want 1", tc.resetHeader, resp.Header.Get(tc.resetHeader))
					}
				}
			}
		})
	}
}

func TestServerStreamsGoldenSSEAndFlushesEveryEvent(t *testing.T) {
	for _, tc := range dialectTestCases {
		t.Run(tc.name, func(t *testing.T) {
			step := Step{
				Status:  http.StatusOK,
				Fixture: filepath.Join(tc.name, "stream.sse"),
				Stream:  true,
			}
			server := New(step)
			defer server.Close()

			resp, err := server.Client().Post(server.URL+tc.streamPath, "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("流式请求失败: %v", err)
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatalf("读取流式响应失败: %v", readErr)
			}
			if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
				t.Errorf("Content-Type = %q, want text/event-stream", got)
			}
			assertFixtureBody(t, body, tc.name, "stream.sse")
			if !bytes.Contains(body, []byte(tc.streamUsageMarker)) {
				t.Errorf("流式 fixture 缺少 usage 事件标记 %s", tc.streamUsageMarker)
			}

			flushServer := New(step)
			defer flushServer.Close()
			recorder := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
			req := httptest.NewRequest(http.MethodPost, tc.streamPath, strings.NewReader(`{}`))
			flushServer.ServeHTTP(recorder, req)

			golden := readFixture(t, tc.name, "stream.sse")
			wantFlushes := countSSEEvents(golden)
			if recorder.flushes != wantFlushes {
				t.Errorf("Flush 次数 = %d, want %d（每个 SSE 事件一次）", recorder.flushes, wantFlushes)
			}
		})
	}
}

func TestServerSupportsDelay(t *testing.T) {
	const delay = 80 * time.Millisecond
	server := New(Step{
		Status:  http.StatusOK,
		Fixture: filepath.Join("openai", "success.json"),
		Delay:   delay,
	})
	defer server.Close()

	startedAt := time.Now()
	resp, err := server.Client().Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("慢响应请求失败: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	elapsed := time.Since(startedAt)
	if elapsed < delay {
		t.Errorf("响应耗时 = %s, want >= %s", elapsed, delay)
	}
}

func TestServerSupportsConnectionDrop(t *testing.T) {
	server := New(Step{DropConn: true})
	defer server.Close()
	server.Client().Timeout = time.Second

	resp, err := server.Client().Post(server.URL+"/v1/messages", "application/json", strings.NewReader(`{}`))
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("断连脚本未让客户端收到错误")
	}
	if len(server.Requests()) != 1 {
		t.Fatalf("断连请求也应被记录，got %d 条", len(server.Requests()))
	}
}

func TestServerRejectsUnsupportedMethodAndPathWithoutConsumingScript(t *testing.T) {
	server := New(Step{Status: http.StatusOK, Fixture: filepath.Join("openai", "success.json")})
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET 请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET 状态 = %d, want 405", resp.StatusCode)
	}

	resp, err = server.Client().Post(server.URL+"/unknown", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("未知路径请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("未知路径状态 = %d, want 404", resp.StatusCode)
	}

	resp, err = server.Client().Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("有效请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("无效请求不应消耗脚本，有效请求状态 = %d, want 200", resp.StatusCode)
	}
	if len(server.Requests()) != 3 {
		t.Errorf("所有收到的请求都应记录，got %d 条", len(server.Requests()))
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

func assertFixtureBody(t *testing.T, got []byte, dialect, name string) {
	t.Helper()
	want := readFixture(t, dialect, name)
	if !bytes.Equal(got, want) {
		t.Errorf("响应体与 golden fixture 不一致\ngot:  %s\nwant: %s", got, want)
	}
}

func readFixture(t *testing.T, dialect, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", dialect, name))
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	return data
}

func countSSEEvents(data []byte) int {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n\n"))
}
