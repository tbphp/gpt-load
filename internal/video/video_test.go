package video

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCreateRequestAppliesDefaultsAndModeRules(t *testing.T) {
	t.Parallel()
	valid := []struct {
		name string
		body string
	}{
		{name: "text defaults", body: `{"model":"m","prompt":"p"}`},
		{name: "keyframe first", body: `{"model":"m","prompt":"p","mode":"keyframe","first_frame":"https://example.com/a.png"}`},
		{name: "keyframe last", body: `{"model":"m","prompt":"p","mode":"keyframe","last_frame":"https://example.com/b.png"}`},
		{name: "reference images", body: `{"model":"m","prompt":"p","mode":"reference","images":["https://example.com/a.png"]}`},
		{name: "reference audios", body: `{"model":"m","prompt":"p","mode":"reference","audios":["https://example.com/a.mp3"]}`},
		{name: "reference videos", body: `{"model":"m","prompt":"p","mode":"reference","videos":[{"url":"https://example.com/a.mp4","start_seconds":1.5,"require_audio":true}]}`},
		{name: "seconds bounds", body: `{"model":"m","prompt":"p","seconds":"4"}`},
		{name: "size 2K", body: `{"model":"m","prompt":"p","size":"2K"}`},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseCreateRequest([]byte(test.body))
			if err != nil {
				t.Fatalf("ParseCreateRequest() error = %v", err)
			}
			if parsed.Model != "m" || parsed.Prompt != "p" {
				t.Fatalf("parsed = %#v", parsed)
			}
			if parsed.NormalizedBody == nil {
				t.Fatal("normalized body is nil")
			}
		})
	}

	text, err := ParseCreateRequest([]byte(`{"model":"m","prompt":"p"}`))
	if err != nil {
		t.Fatal(err)
	}
	if text.Mode != "text" || text.Seconds != "5" || text.Size != "720P" || text.AspectRatio != "16:9" || text.N != 1 {
		t.Fatalf("defaults = %#v", text)
	}
	for _, field := range []string{`"mode":"text"`, `"seconds":"5"`, `"size":"720P"`, `"aspect_ratio":"16:9"`, `"n":1`} {
		if !strings.Contains(string(text.NormalizedBody), field) {
			t.Fatalf("normalized body %s missing %s", text.NormalizedBody, field)
		}
	}
}

func TestParseCreateRequestRejectsInvalidShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		param string
	}{
		{name: "not object", body: `[]`, param: ""},
		{name: "missing model", body: `{"prompt":"p"}`, param: "model"},
		{name: "missing prompt", body: `{"model":"m"}`, param: "prompt"},
		{name: "bad mode", body: `{"model":"m","prompt":"p","mode":"other"}`, param: "mode"},
		{name: "text with first frame", body: `{"model":"m","prompt":"p","first_frame":"https://example.com/a.png"}`, param: "mode"},
		{name: "keyframe without frames", body: `{"model":"m","prompt":"p","mode":"keyframe"}`, param: "first_frame"},
		{name: "keyframe with images", body: `{"model":"m","prompt":"p","mode":"keyframe","first_frame":"https://example.com/a.png","images":["https://example.com/a.png"]}`, param: "mode"},
		{name: "reference without media", body: `{"model":"m","prompt":"p","mode":"reference"}`, param: "images"},
		{name: "reference with frame", body: `{"model":"m","prompt":"p","mode":"reference","first_frame":"https://example.com/a.png"}`, param: "mode"},
		{name: "seconds not string", body: `{"model":"m","prompt":"p","seconds":5}`, param: "seconds"},
		{name: "seconds below range", body: `{"model":"m","prompt":"p","seconds":"3"}`, param: "seconds"},
		{name: "seconds above range", body: `{"model":"m","prompt":"p","seconds":"13"}`, param: "seconds"},
		{name: "bad size", body: `{"model":"m","prompt":"p","size":"4K"}`, param: "size"},
		{name: "n not one", body: `{"model":"m","prompt":"p","n":2}`, param: "n"},
		{name: "too many images", body: `{"model":"m","prompt":"p","mode":"reference","images":["https://e.com/1","https://e.com/2","https://e.com/3","https://e.com/4","https://e.com/5","https://e.com/6","https://e.com/7","https://e.com/8","https://e.com/9"]}`, param: "images"},
		{name: "too many videos", body: `{"model":"m","prompt":"p","mode":"reference","videos":[{"url":"https://e.com/a"},{"url":"https://e.com/b"}]}`, param: "videos"},
		{name: "bad media url", body: `{"model":"m","prompt":"p","mode":"reference","images":["file:///etc/passwd"]}`, param: "images"},
		{name: "video missing url", body: `{"model":"m","prompt":"p","mode":"reference","videos":[{"start_seconds":1}]}`, param: "videos"},
		{name: "video empty url", body: `{"model":"m","prompt":"p","mode":"reference","videos":[{"url":""}]}`, param: "videos"},
		{name: "seed not integer", body: `{"model":"m","prompt":"p","seed":"abc"}`, param: "seed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCreateRequest([]byte(test.body))
			if err == nil {
				t.Fatal("ParseCreateRequest() error = nil")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want ValidationError", err)
			}
			if validation.Param != test.param {
				t.Fatalf("param = %q, want %q (%v)", validation.Param, test.param, err)
			}
		})
	}
}

func TestApplyUpstreamObservationPreservesStatusWhenUpstreamOmitsIt(t *testing.T) {
	t.Parallel()
	task := Task{Status: StatusCompleted, Progress: 100, ResultURL: "https://cdn.example/a.mp4"}
	updated := ApplyUpstreamObservation(task, UpstreamResponse{})
	if updated.Status != StatusCompleted || updated.Progress != 100 || updated.ResultURL != "https://cdn.example/a.mp4" {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestNormalizeStatusMapsProviderStates(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":           StatusQueued,
		"queued":     StatusQueued,
		"pending":    StatusInProgress,
		"processing": StatusInProgress,
		"running":    StatusInProgress,
		"done":       StatusCompleted,
		"completed":  StatusCompleted,
		"succeeded":  StatusCompleted,
		"error":      StatusFailed,
		"failed":     StatusFailed,
		"cancelled":  StatusCancelled,
		"canceled":   StatusCancelled,
		"unexpected": StatusInProgress,
	}
	for input, want := range cases {
		if got := NormalizeStatus(input); got != want {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSelectUpstreamTaskIDPrefersProviderIdentity(t *testing.T) {
	t.Parallel()
	response := UpstreamResponse{ID: "id-1", TaskID: "task-1", VideoID: "video-1"}
	if got := SelectUpstreamTaskID("openai", response); got != "id-1" {
		t.Fatalf("openai id = %q", got)
	}
	if got := SelectUpstreamTaskID("agnes", response); got != "video-1" {
		t.Fatalf("agnes id = %q", got)
	}
	response.VideoID = ""
	if got := SelectUpstreamTaskID("agnes", response); got != "task-1" {
		t.Fatalf("agnes fallback id = %q", got)
	}
	response.TaskID = ""
	if got := SelectUpstreamTaskID("agnes", response); got != "id-1" {
		t.Fatalf("agnes final fallback id = %q", got)
	}
}

func TestParseUpstreamResponseKeepsURLAndError(t *testing.T) {
	t.Parallel()
	response, err := ParseUpstreamResponse([]byte(`{"id":"video_1","status":"completed","progress":100,"url":"https://cdn.example/v.mp4"}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "completed" || response.URL != "https://cdn.example/v.mp4" || response.Progress == nil || *response.Progress != 100 {
		t.Fatalf("response = %#v", response)
	}
	response, err = ParseUpstreamResponse([]byte(`{"id":"video_2","status":"failed","error":{"code":"upstream_error","message":"boom"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.ErrorCode != "upstream_error" || response.ErrorMessage != "boom" {
		t.Fatalf("error response = %#v", response)
	}
	if _, err := ParseUpstreamResponse([]byte(`not json`)); err == nil {
		t.Fatal("malformed upstream response was accepted")
	}
}

func TestIdempotencyHashScopesToAccessKey(t *testing.T) {
	t.Parallel()
	first, err := IdempotencyHash(1, "key")
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdempotencyHash(2, "key")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 64 {
		t.Fatalf("hashes = %q, %q", first, second)
	}
	if _, err := IdempotencyHash(1, strings.Repeat("x", 300)); err == nil {
		t.Fatal("oversized idempotency key was accepted")
	}
}

func TestApplyUpstreamObservationCompletedForcesProgress100(t *testing.T) {
	t.Parallel()
	progress := 42
	updated := ApplyUpstreamObservation(Task{Status: StatusInProgress, Progress: 42}, UpstreamResponse{
		Status: "completed", Progress: &progress,
	})
	if updated.Status != StatusCompleted || updated.Progress != 100 {
		t.Fatalf("updated = %#v", updated)
	}
}
