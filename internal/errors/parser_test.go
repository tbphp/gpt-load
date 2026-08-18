package errors

import "testing"

func TestParseUpstreamErrorArrearage(t *testing.T) {
	// 阿里云欠费响应的真实结构(实测)
	body := []byte(`{"error":{"message":"Access denied, please make sure your account is in good standing. For details, see: https://help.aliyun.com/zh/model-studio/error-code#overdue-payment","type":"Arrearage","param":null,"code":"Arrearage"},"id":"chatcmpl-1","request_id":"r1"}`)
	got := ParseUpstreamError(body)
	want := "Access denied, please make sure your account is in good standing."
	if got[:len(want)] != want {
		t.Fatalf("unexpected parse result: %q", got)
	}
}

func TestParseUpstreamErrorEmptyBody(t *testing.T) {
	// 空 body 应返回空字符串(调用方负责兜底)
	if got := ParseUpstreamError([]byte("")); got != "" {
		t.Fatalf("empty body should yield empty string, got %q", got)
	}
	if got := ParseUpstreamError([]byte("   \n")); got != "   \n" {
		t.Fatalf("whitespace body should degrade to raw truncated body, got %q", got)
	}
}

func TestParseUpstreamErrorStandardOpenAI(t *testing.T) {
	body := []byte(`{"error":{"message":"Model access denied.","type":"invalid_request_error","code":"model_access_denied"}}`)
	if got := ParseUpstreamError(body); got != "Model access denied." {
		t.Fatalf("unexpected parse result: %q", got)
	}
}