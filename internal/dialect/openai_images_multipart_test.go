package dialect

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
)

type multipartTestPart struct {
	header textproto.MIMEHeader
	body   string
}

func TestOpenAIImagesMultipartInspectAndRewrite(t *testing.T) {
	t.Parallel()

	modelHeader := textproto.MIMEHeader{
		"Content-Disposition":       {`form-data; name="model"`},
		"Content-Type":              {"text/plain"},
		"Content-Length":            {"12"},
		"Content-Transfer-Encoding": {"quoted-printable"},
		"ETag":                      {`"model-v1"`},
		"Digest":                    {"sha-256=model"},
		"Content-MD5":               {"model-md5"},
		"Content-Range":             {"bytes 0-11/12"},
		"Content-Digest":            {"sha-256=:bW9kZWw=:"},
		"Repr-Digest":               {"sha-256=:cmVwcg==:"},
		"Signature":                 {"sig=:c2ln:"},
		"Signature-Input":           {`sig=("content-digest")`},
	}
	body, contentType := buildMultipartForTest(t, []multipartTestPart{
		{header: textproto.MIMEHeader{
			"Content-Disposition":       {`form-data; name="image"; filename="../original.png"`},
			"Content-Type":              {"image/png"},
			"X-Future-Header":           {"keep-me"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		}, body: "=41=42=43"},
		{header: modelHeader, body: "public-image"},
		{header: textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="stream"`},
		}, body: "true"},
		{header: textproto.MIMEHeader{
			"Content-Disposition": {`form-data; name="future"`},
			"X-Unknown":           {"one", "two"},
		}, body: "future-value"},
	})
	request := &ParsedRequest{
		Method: http.MethodPost,
		Path:   openAIImagesEditsPath,
		Header: http.Header{
			"Content-Type":   {contentType},
			"Content-Length": {strconv.Itoa(len(body))},
			"Digest":         {"sha-256=outer"},
		},
		Body: body,
	}

	metadata, err := NewOpenAIImages().InspectRequest(request)
	if err != nil {
		t.Fatalf("InspectRequest() error = %v", err)
	}
	if metadata.Model == nil || *metadata.Model != "public-image" || !metadata.Stream {
		t.Fatalf("metadata = %#v", metadata)
	}

	rewritten, err := NewOpenAIImages().RewriteRequestModel(request, "upstream-image")
	if err != nil {
		t.Fatalf("RewriteRequestModel() error = %v", err)
	}
	if bytes.Equal(rewritten.Body, request.Body) {
		t.Fatal("multipart body was not rebuilt")
	}
	if rewritten.Header.Get("Content-Type") == contentType ||
		rewritten.Header.Get("Content-Length") != strconv.Itoa(len(rewritten.Body)) {
		t.Fatalf("rewritten headers = %#v", rewritten.Header)
	}
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest",
		"Repr-Digest", "Signature", "Signature-Input",
	} {
		if rewritten.Header.Get(name) != "" {
			t.Fatalf("outer %s survived rewrite", name)
		}
	}

	parts := readMultipartForTest(t, rewritten.Body, rewritten.Header.Get("Content-Type"))
	if len(parts) != 4 {
		t.Fatalf("part count = %d", len(parts))
	}
	if got := parts[0].header.Get("Content-Disposition"); got != `form-data; name="image"; filename="../original.png"` {
		t.Fatalf("image Content-Disposition = %q", got)
	}
	if parts[0].header.Get("Content-Transfer-Encoding") != "quoted-printable" ||
		parts[0].header.Get("X-Future-Header") != "keep-me" || parts[0].body != "=41=42=43" {
		t.Fatalf("image part = %#v", parts[0])
	}
	if parts[1].body != "upstream-image" {
		t.Fatalf("model body = %q", parts[1].body)
	}
	for _, name := range []string{
		"Content-Length", "Content-Transfer-Encoding", "ETag", "Digest", "Content-MD5",
		"Content-Range", "Content-Digest", "Repr-Digest", "Signature", "Signature-Input",
	} {
		if parts[1].header.Get(name) != "" {
			t.Fatalf("rewritten model %s survived", name)
		}
	}
	if parts[2].body != "true" || parts[3].body != "future-value" ||
		len(parts[3].header.Values("X-Unknown")) != 2 {
		t.Fatalf("unchanged parts = %#v", parts[2:])
	}
	if string(request.Body) != string(body) || request.Header.Get("Content-Type") != contentType {
		t.Fatal("rewrite mutated the original request")
	}
}

func TestOpenAIImagesMultipartValidation(t *testing.T) {
	t.Parallel()

	validModel := multipartTestPart{
		header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="model"`}},
		body:   "gpt-image-2",
	}
	tests := []struct {
		name  string
		parts []multipartTestPart
	}{
		{name: "missing model", parts: []multipartTestPart{{header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="prompt"`}}, body: "draw"}}},
		{name: "duplicate model", parts: []multipartTestPart{validModel, validModel}},
		{name: "invalid stream", parts: []multipartTestPart{validModel, {header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="stream"`}}, body: "1"}}},
		{name: "model file", parts: []multipartTestPart{{header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="model"; filename="model.txt"`}}, body: "gpt-image-2"}}},
		{name: "oversized model", parts: []multipartTestPart{{header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="model"`}}, body: strings.Repeat("m", 4097)}}},
		{name: "too many parts", parts: repeatMultipartParts(validModel, 129)},
		{name: "oversized content disposition", parts: []multipartTestPart{{header: textproto.MIMEHeader{"Content-Disposition": {`form-data; name="` + strings.Repeat("m", 4096) + `"`}}, body: "gpt-image-2"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, contentType := buildMultipartForTest(t, test.parts)
			metadata, err := NewOpenAIImages().InspectRequest(&ParsedRequest{
				Method: http.MethodPost,
				Path:   openAIImagesEditsPath,
				Header: http.Header{"Content-Type": {contentType}},
				Body:   body,
			})
			if err == nil {
				t.Fatalf("InspectRequest() = %#v, nil error", metadata)
			}
		})
	}
}

func buildMultipartForTest(t *testing.T, parts []multipartTestPart) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.SetBoundary("test-boundary"); err != nil {
		t.Fatalf("SetBoundary() error = %v", err)
	}
	for _, part := range parts {
		output, err := writer.CreatePart(part.header)
		if err != nil {
			t.Fatalf("CreatePart() error = %v", err)
		}
		if _, err := io.WriteString(output, part.body); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return bytes.Clone(buffer.Bytes()), writer.FormDataContentType()
}

func readMultipartForTest(t *testing.T, body []byte, contentType string) []multipartTestPart {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType() error = %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parts := make([]multipartTestPart, 0)
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart() error = %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll(part) error = %v", err)
		}
		parts = append(parts, multipartTestPart{
			header: textproto.MIMEHeader(http.Header(part.Header).Clone()),
			body:   string(data),
		})
	}
	return parts
}

func repeatMultipartParts(part multipartTestPart, count int) []multipartTestPart {
	parts := make([]multipartTestPart, count)
	for index := range parts {
		parts[index] = part
		parts[index].header = textproto.MIMEHeader(http.Header(part.header).Clone())
		parts[index].header.Set("Content-Disposition", `form-data; name="part`+strconv.Itoa(index)+`"`)
	}
	return parts
}
