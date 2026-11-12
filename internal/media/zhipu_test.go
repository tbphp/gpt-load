package media

import (
	"strings"
	"testing"
)

func TestParseZhipuImageRequest(t *testing.T) {
	t.Parallel()
	create, err := ParseZhipuImageRequest([]byte(`{"model":"CogView-3-Flash","prompt":"fox"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if create.Protocol != ProtocolZhipuMedia || create.Operation != OperationZhipuImagesGenerate ||
		create.Kind != KindImage || create.Size != "1024x1024" || create.ImageCount != 1 {
		t.Fatalf("create = %#v", create)
	}
	async, err := ParseZhipuImageRequest([]byte(`{"model":"CogView-3-Flash","prompt":"fox","size":"1440x720","quality":"hd"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if async.Operation != OperationZhipuImagesCreateAsync || async.Quality != "hd" {
		t.Fatalf("async = %#v", async)
	}
	for _, body := range []string{
		`{"model":"CogView-3-Flash"}`,
		`{"model":"CogView-3-Flash","prompt":"fox","size":"123x456"}`,
		`{"model":"CogView-3-Flash","prompt":"fox","size":"1024x1024","quality":"ultra"}`,
	} {
		if _, err := ParseZhipuImageRequest([]byte(body), false); err == nil {
			t.Fatalf("invalid image request accepted: %s", body)
		}
	}
}

func TestParseZhipuVideoRequest(t *testing.T) {
	t.Parallel()
	create, err := ParseZhipuVideoRequest([]byte(`{"model":"CogVideoX-Flash","prompt":"fox","image_url":"data:image/png;base64,aGVsbG8=","size":"1280x720","fps":60,"quality":"speed","duration":10,"with_audio":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if create.Operation != OperationZhipuVideosCreate || create.Kind != KindVideo ||
		create.FPS != 60 || create.Duration != 10 || create.WithAudio || create.Quality != "speed" {
		t.Fatalf("create = %#v", create)
	}
	for _, body := range []string{
		`{"model":"CogVideoX-Flash"}`,
		`{"model":"CogVideoX-Flash","prompt":"fox","fps":45}`,
		`{"model":"CogVideoX-Flash","prompt":"fox","duration":15}`,
		`{"model":"CogVideoX-Flash","prompt":"fox","image_url":"file:///tmp/a.png"}`,
	} {
		if _, err := ParseZhipuVideoRequest([]byte(body)); err == nil {
			t.Fatalf("invalid video request accepted: %s", body)
		}
	}
}

func TestParseZhipuResponseAndTaskResponse(t *testing.T) {
	t.Parallel()
	response, err := ParseZhipuResponse([]byte(`{"id":"task-1","request_id":"req-1","model":"CogView-3-Flash","task_status":"SUCCESS","image_result":[{"url":"https://cdn.example/i.png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if SelectZhipuTaskID(response) != "task-1" || response.TaskStatus != "SUCCESS" {
		t.Fatalf("response = %#v", response)
	}
	task := ApplyZhipuObservation(Task{Kind: KindImage, Status: StatusInProgress}, response)
	if task.Status != StatusCompleted || task.Progress != 100 || task.ResultURL != "https://cdn.example/i.png" {
		t.Fatalf("task = %#v", task)
	}
	body := ZhipuTaskResponse(task, response)
	results, ok := body["image_result"].([]ZhipuResult)
	if !ok || len(results) != 1 || results[0].URL != "https://cdn.example/i.png" {
		t.Fatalf("response body = %#v", body)
	}
	if _, ok := body["data"].([]ZhipuResult); !ok {
		t.Fatalf("data mirror missing: %#v", body)
	}

	video, err := ParseZhipuResponse([]byte(`{"id":"v-1","task_status":"FAIL","error":{"code":"bad","message":"boom"}}`))
	if err != nil {
		t.Fatal(err)
	}
	task = ApplyZhipuObservation(Task{Kind: KindVideo, Status: StatusInProgress}, video)
	if task.Status != StatusFailed || task.LastErrorCode != "bad" || task.LastErrorMessage != "boom" {
		t.Fatalf("failed task = %#v", task)
	}
}

func TestNormalizeStatusHandlesZhipuStates(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"PROCESSING": StatusInProgress,
		"SUCCESS":    StatusCompleted,
		"FAIL":       StatusFailed,
		"CANCELLED":  StatusCancelled,
	}
	for input, want := range cases {
		if got := NormalizeStatus(input); got != want {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := ParseZhipuResponse([]byte(`not json`)); err == nil || !strings.Contains(err.Error(), "not a JSON") {
		t.Fatalf("error = %v", err)
	}
}
