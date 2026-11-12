package dialect

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/media"
)

func TestOpenAIVideosInspectRequest(t *testing.T) {
	t.Parallel()
	d := NewOpenAIVideos()
	request := &ParsedRequest{
		Method: http.MethodPost, Path: "/v1/videos", Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"model":"agnes-video-2.5-flash","prompt":"fox","seconds":"5"}`),
	}
	metadata, err := d.InspectRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Operation != execution.OperationVideosCreate || metadata.Model == nil ||
		*metadata.Model != "agnes-video-2.5-flash" || metadata.MediaCreate == nil ||
		metadata.RouteRequirement != execution.RouteRequirementNative {
		t.Fatalf("create metadata = %#v", metadata)
	}
	if !strings.Contains(string(request.Body), `"mode":"text"`) {
		t.Fatalf("create body was not normalized: %s", request.Body)
	}

	cases := []struct {
		method    string
		path      string
		operation execution.Operation
		taskID    string
	}{
		{method: http.MethodGet, path: "/v1/videos/video_abc", operation: execution.OperationVideosRetrieve, taskID: "video_abc"},
		{method: http.MethodGet, path: "/v1/videos/video_abc/content", operation: execution.OperationVideosContent, taskID: "video_abc"},
		{method: http.MethodDelete, path: "/v1/videos/video_abc", operation: execution.OperationVideosDelete, taskID: "video_abc"},
	}
	for _, test := range cases {
		metadata, err := d.InspectRequest(&ParsedRequest{Method: test.method, Path: test.path})
		if err != nil {
			t.Fatalf("%s %s: %v", test.method, test.path, err)
		}
		if metadata.Operation != test.operation || metadata.MediaTaskID != test.taskID {
			t.Fatalf("%s %s metadata = %#v", test.method, test.path, metadata)
		}
	}

	for _, invalid := range []*ParsedRequest{
		{Method: http.MethodGet, Path: "/v1/videos"},
		{Method: http.MethodPost, Path: "/v1/videos/video_abc"},
		{Method: http.MethodGet, Path: "/v1/videos/../content"},
		{Method: http.MethodGet, Path: "/v1/videos/video_abc/other"},
		{Method: http.MethodPost, Path: "/v1/videos", Body: []byte(`{"model":"m"}`)},
	} {
		if _, err := d.InspectRequest(invalid); err == nil {
			t.Fatalf("invalid request %s %s was accepted", invalid.Method, invalid.Path)
		}
	}
}

func TestOpenAIVideosValidationErrorType(t *testing.T) {
	t.Parallel()
	_, err := NewOpenAIVideos().InspectRequest(&ParsedRequest{
		Method: http.MethodPost, Path: "/v1/videos",
		Body: []byte(`{"model":"m","prompt":"p","mode":"keyframe"}`),
	})
	var validation *media.ValidationError
	if !errors.As(err, &validation) || validation.Param != "first_frame" {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAIVideosSanitizeRewritesModel(t *testing.T) {
	t.Parallel()
	request := &ParsedRequest{
		Method: http.MethodPost, Path: "/v1/videos", Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"model":"client-model","prompt":"fox"}`),
	}
	sanitized, err := NewOpenAIVideos().SanitizeRequestForAttempt(request, "upstream-model", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sanitized.Body), `"model":"upstream-model"`) {
		t.Fatalf("sanitized body = %s", sanitized.Body)
	}
	if strings.Contains(string(request.Body), "upstream-model") {
		t.Fatal("original request was mutated")
	}
}
