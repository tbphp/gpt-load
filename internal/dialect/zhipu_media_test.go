package dialect

import (
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/media"
)

func TestZhipuMediaInspectRequest(t *testing.T) {
	t.Parallel()
	d := NewZhipuMedia()
	cases := []struct {
		method    string
		path      string
		body      string
		operation execution.Operation
	}{
		{http.MethodPost, "/v1/images/generations", `{"model":"CogView-3-Flash","prompt":"fox"}`, execution.OperationZhipuImagesGenerate},
		{http.MethodPost, "/v1/async/images/generations", `{"model":"CogView-3-Flash","prompt":"fox"}`, execution.OperationZhipuImagesCreateAsync},
		{http.MethodPost, "/v1/videos/generations", `{"model":"CogVideoX-Flash","prompt":"fox"}`, execution.OperationZhipuVideosCreate},
		{http.MethodGet, "/v1/async-result/zhipu_abc", "", execution.OperationZhipuMediaRetrieve},
		{http.MethodDelete, "/v1/async-result/zhipu_abc", "", execution.OperationZhipuMediaCancel},
	}
	for _, test := range cases {
		metadata, err := d.InspectRequest(&ParsedRequest{Method: test.method, Path: test.path, Body: []byte(test.body)})
		if err != nil {
			t.Fatalf("%s %s: %v", test.method, test.path, err)
		}
		if metadata.Operation != test.operation || metadata.RouteRequirement != execution.RouteRequirementNative {
			t.Fatalf("%s %s metadata = %#v", test.method, test.path, metadata)
		}
		if test.operation == execution.OperationZhipuMediaRetrieve || test.operation == execution.OperationZhipuMediaCancel {
			if metadata.MediaTaskID != "zhipu_abc" {
				t.Fatalf("task id = %q", metadata.MediaTaskID)
			}
		}
	}
	if metadata, err := d.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1/async/images/generations", Body: []byte(`{"model":"CogView-3-Flash","prompt":"fox"}`)}); err != nil {
		t.Fatal(err)
	} else if metadata.MediaCreate == nil || metadata.MediaCreate.Operation != media.OperationZhipuImagesCreateAsync {
		t.Fatalf("create = %#v", metadata.MediaCreate)
	}
	for _, invalid := range []*ParsedRequest{
		{Method: http.MethodGet, Path: "/v1/images/generations"},
		{Method: http.MethodPost, Path: "/v1/async-result/zhipu_abc"},
		{Method: http.MethodGet, Path: "/v1/async-result/"},
		{Method: http.MethodGet, Path: "/v1/async-result/a/b"},
		{Method: http.MethodPost, Path: "/v1/videos/generations", Body: []byte(`{"model":"v"}`)},
	} {
		if _, err := d.InspectRequest(invalid); err == nil {
			t.Fatalf("invalid request %s %s accepted", invalid.Method, invalid.Path)
		}
	}
}

func TestZhipuMediaSanitizeRewritesModel(t *testing.T) {
	t.Parallel()
	d := NewZhipuMedia()
	request := &ParsedRequest{
		Method: http.MethodPost, Path: "/v1/videos/generations", Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"model":"CogVideoX-Flash","prompt":"fox","fps":30}`),
	}
	sanitized, err := d.SanitizeRequestForAttempt(request, "cogvideox-flash", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sanitized.Body), `"model":"cogvideox-flash"`) || strings.Contains(string(request.Body), "cogvideox-flash") {
		t.Fatalf("sanitized body = %s original = %s", sanitized.Body, request.Body)
	}
}
