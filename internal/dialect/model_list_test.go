package dialect

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}

func TestModelListCollectorPreservesFirstOccurrenceOrder(t *testing.T) {
	collector := newModelListCollector()
	if err := collector.Add([]string{"claude-a", "Shared", "claude-a"}); err != nil {
		t.Fatalf("first Add() error = %v", err)
	}
	if err := collector.Add([]string{"shared", "Shared", "claude-b"}); err != nil {
		t.Fatalf("second Add() error = %v", err)
	}

	result := collector.Result()
	if got, want := fmt.Sprint(result), "[claude-a Shared shared claude-b]"; got != want {
		t.Fatalf("Result() = %s, want %s", got, want)
	}
	result[0] = "mutated"
	if got := collector.Result()[0]; got != "claude-a" {
		t.Fatalf("Result() exposed collector storage: first value = %q", got)
	}
}

func TestModelListCollectorRejectsMoreThanMaximumUniqueModels(t *testing.T) {
	collector := newModelListCollector()
	values := make([]string, maxUniqueModelListEntries)
	for index := range values {
		values[index] = fmt.Sprintf("model-%06d", index)
	}
	if err := collector.Add(values); err != nil {
		t.Fatalf("Add(exact maximum) error = %v", err)
	}
	if !collector.Full() {
		t.Fatal("Full() = false at exact maximum")
	}
	if err := collector.Add([]string{values[0]}); err != nil {
		t.Fatalf("Add(duplicate at maximum) error = %v", err)
	}
	if err := collector.Add([]string{"one-too-many"}); err == nil {
		t.Fatal("Add(100001st unique model) error = nil")
	}
	if got := len(collector.Result()); got != maxUniqueModelListEntries {
		t.Fatalf("Result() length = %d, want %d", got, maxUniqueModelListEntries)
	}
}

func TestModelListCollectorBoundsJSONEncodedBytes(t *testing.T) {
	if maxUniqueModelListJSONBytes != 16<<20 {
		t.Fatalf("maxUniqueModelListJSONBytes = %d", maxUniqueModelListJSONBytes)
	}
	collector := newModelListCollector()
	collector.maxJSONBytes = 12 // [] + "1234" + , + "x" = 12
	if err := collector.Add([]string{"1234", "x"}); err != nil {
		t.Fatalf("Add(exact encoded limit) error = %v", err)
	}
	if err := collector.Add([]string{"x"}); err != nil {
		t.Fatalf("duplicate changed encoded budget: %v", err)
	}
	if err := collector.Add([]string{"y"}); err == nil {
		t.Fatal("Add(limit+1 model) error = nil")
	}

	escaped := newModelListCollector()
	escaped.maxJSONBytes = 8 // ["\n"] is exactly 6; another item exceeds 8.
	if err := escaped.Add([]string{"\n"}); err != nil {
		t.Fatal(err)
	}
	if err := escaped.Add([]string{"z"}); err == nil {
		t.Fatal("escaped JSON bytes were counted as raw UTF-8")
	}
}

func TestDecodeModelListPageRequiresBoundedSingleIdentityJSON(t *testing.T) {
	if maxModelListPageBytes != 8<<20 {
		t.Fatalf("maxModelListPageBytes = %d", maxModelListPageBytes)
	}
	valid := `{"data":[{"id":"a"}]}`
	tests := []struct {
		name      string
		body      string
		encoding  []string
		wantError bool
	}{
		{name: "missing encoding", body: valid},
		{name: "identity", body: valid + "  ", encoding: []string{"identity"}},
		{name: "second JSON", body: valid + `{}`, wantError: true},
		{name: "gzip", body: valid, encoding: []string{"gzip"}, wantError: true},
		{name: "multiple encodings", body: valid, encoding: []string{"identity", "identity"}, wantError: true},
		{name: "blank encoding", body: valid, encoding: []string{" "}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), ContentLength: int64(len(test.body))}
			for _, value := range test.encoding {
				response.Header.Add("Content-Encoding", value)
			}
			target := struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				Marker string `json:"marker"`
			}{Marker: "unchanged"}
			err := decodeModelListPage(response, &target)
			if test.wantError == (err == nil) {
				t.Fatalf("decodeModelListPage() error = %v", err)
			}
			if test.wantError && target.Marker != "unchanged" {
				t.Fatalf("error returned partial target = %#v", target)
			}
		})
	}
}

func TestDecodeModelListPageAcceptsExactKnownAndStreamingLimit(t *testing.T) {
	body := `{}` + strings.Repeat(" ", int(maxModelListPageBytes)-2)
	for _, contentLength := range []int64{maxModelListPageBytes, -1} {
		response := &http.Response{
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: contentLength,
		}
		var target struct{}
		if err := decodeModelListPage(response, &target); err != nil {
			t.Fatalf("decodeModelListPage(ContentLength=%d) error = %v", contentLength, err)
		}
	}
}

func TestDecodeModelListPageRejectsKnownAndStreamingOverflow(t *testing.T) {
	tests := []struct {
		name          string
		contentLength int64
		body          io.Reader
	}{
		{
			name:          "known length",
			contentLength: maxModelListPageBytes + 1,
			body:          strings.NewReader("{}"),
		},
		{
			name:          "streaming length",
			contentLength: -1,
			body:          io.LimitReader(zeroReader{}, maxModelListPageBytes+1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := struct {
				Marker string `json:"marker"`
			}{Marker: "unchanged"}
			response := &http.Response{
				Header:        make(http.Header),
				Body:          io.NopCloser(test.body),
				ContentLength: test.contentLength,
			}
			if err := decodeModelListPage(response, &target); err == nil {
				t.Fatal("decodeModelListPage() error = nil")
			}
			if target.Marker != "unchanged" {
				t.Fatalf("partial target = %#v", target)
			}
		})
	}
}
