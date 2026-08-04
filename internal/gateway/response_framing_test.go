package gateway

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestClassifyResponseBody(t *testing.T) {
	tests := []struct {
		name   string
		method string
		status int
		want   responseBodyPolicy
	}{
		{name: "ordinary response", method: http.MethodGet, status: http.StatusOK, want: responseBodyPolicy{readBody: true, writeBody: true}},
		{name: "head", method: http.MethodHead, status: http.StatusOK, want: responseBodyPolicy{preserveContentLength: true}},
		{name: "informational status overrides head", method: http.MethodHead, status: http.StatusContinue, want: responseBodyPolicy{}},
		{name: "no content", method: http.MethodGet, status: http.StatusNoContent, want: responseBodyPolicy{}},
		{name: "reset content", method: http.MethodGet, status: http.StatusResetContent, want: responseBodyPolicy{}},
		{name: "not modified", method: http.MethodGet, status: http.StatusNotModified, want: responseBodyPolicy{preserveContentLength: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyResponseBody(test.method, test.status); got != test.want {
				t.Fatalf("classifyResponseBody(%q, %d) = %#v, want %#v", test.method, test.status, got, test.want)
			}
		})
	}
}

func TestNormalizeBufferedResponse(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		status            int
		headers           http.Header
		body              []byte
		wantWriteBody     bool
		wantContentLength string
	}{
		{
			name:   "bodyful response gets plaintext length",
			method: http.MethodGet,
			status: http.StatusOK,
			headers: http.Header{
				"content-encoding": {"gzip"},
				"Content-Length":   {"100"},
			},
			body:              []byte("plain"),
			wantWriteBody:     true,
			wantContentLength: "5",
		},
		{
			name:   "forwarded head preserves legal representation length",
			method: http.MethodHead,
			status: http.StatusOK,
			headers: http.Header{
				"Content-Encoding": {"identity"},
				"Content-Length":   {"123"},
			},
			wantContentLength: "123",
		},
		{
			name:   "local head representation uses body length",
			method: http.MethodHead,
			status: http.StatusBadGateway,
			headers: http.Header{
				"Content-Length": {"999"},
			},
			body:              []byte("reason"),
			wantContentLength: "6",
		},
		{
			name:   "informational status deletes length",
			method: http.MethodGet,
			status: http.StatusContinue,
			headers: http.Header{
				"Content-Length": {"8"},
			},
		},
		{
			name:   "no content deletes length",
			method: http.MethodGet,
			status: http.StatusNoContent,
			headers: http.Header{
				"Content-Length": {"8"},
			},
		},
		{
			name:   "reset content permits zero length",
			method: http.MethodGet,
			status: http.StatusResetContent,
			headers: http.Header{
				"Content-Length": {"0"},
			},
			wantContentLength: "0",
		},
		{
			name:   "reset content deletes nonzero length",
			method: http.MethodGet,
			status: http.StatusResetContent,
			headers: http.Header{
				"Content-Length": {"8"},
			},
		},
		{
			name:   "not modified preserves legal representation length",
			method: http.MethodGet,
			status: http.StatusNotModified,
			headers: http.Header{
				"content-encoding": {"identity"},
				"Content-Length":   {"456"},
			},
			wantContentLength: "456",
		},
		{
			name:   "preservation rejects duplicate casing",
			method: http.MethodHead,
			status: http.StatusOK,
			headers: http.Header{
				"Content-Length": {"12"},
				"content-length": {"12"},
			},
		},
		{
			name:   "preservation rejects duplicate values",
			method: http.MethodGet,
			status: http.StatusNotModified,
			headers: http.Header{
				"Content-Length": {"12", "12"},
			},
		},
		{
			name:   "preservation rejects comma length",
			method: http.MethodHead,
			status: http.StatusOK,
			headers: http.Header{
				"Content-Length": {"12, 12"},
			},
		},
		{
			name:   "preservation rejects negative length",
			method: http.MethodGet,
			status: http.StatusNotModified,
			headers: http.Header{
				"Content-Length": {"-1"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := test.headers.Clone()
			got, writeBody := normalizeBufferedResponse(test.method, test.status, source, test.body)
			if writeBody != test.wantWriteBody {
				t.Fatalf("write body = %t, want %t", writeBody, test.wantWriteBody)
			}
			if values := headerFieldValues(got, "Content-Encoding"); len(values) != 0 {
				t.Fatalf("Content-Encoding values = %#v, want absent", values)
			}
			if values := headerFieldValues(got, "Content-Length"); test.wantContentLength == "" {
				if len(values) != 0 {
					t.Fatalf("Content-Length values = %#v, want absent", values)
				}
			} else if !reflect.DeepEqual(values, []string{test.wantContentLength}) {
				t.Fatalf("Content-Length values = %#v, want [%s]", values, test.wantContentLength)
			}
			if !reflect.DeepEqual(source, test.headers) {
				t.Fatalf("normalizeBufferedResponse mutated source headers: got %#v, want %#v", source, test.headers)
			}
		})
	}
}

func TestNormalizeBufferedResponseFiltersContentRangeByStatus(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		headers          http.Header
		body             []byte
		wantContentRange string
	}{
		{
			name:   "ordinary success has no range semantics",
			status: http.StatusOK,
			headers: http.Header{
				"Content-Range": {"bytes 0-2/3"},
			},
			body: []byte("abc"),
		},
		{
			name:   "not modified has no range semantics",
			status: http.StatusNotModified,
			headers: http.Header{
				"Content-Range": {"bytes 0-2/3"},
			},
		},
		{
			name:   "partial content preserves range",
			status: http.StatusPartialContent,
			headers: http.Header{
				"Content-Range": {"bytes 0-2/3"},
			},
			body:             []byte("abc"),
			wantContentRange: "bytes 0-2/3",
		},
		{
			name:   "range not satisfiable preserves valid unsatisfied range",
			status: http.StatusRequestedRangeNotSatisfiable,
			headers: http.Header{
				"Content-Range": {"bytes */3"},
			},
			wantContentRange: "bytes */3",
		},
		{
			name:   "range not satisfiable removes malformed range",
			status: http.StatusRequestedRangeNotSatisfiable,
			headers: http.Header{
				"Content-Range": {"bytes 0-2/3"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotHeaders, _ := normalizeBufferedResponse(http.MethodGet, test.status, test.headers, test.body)
			if got := gotHeaders.Get("Content-Range"); got != test.wantContentRange {
				t.Fatalf("Content-Range = %q, want %q; headers=%#v", got, test.wantContentRange, gotHeaders)
			}
		})
	}
}

func TestNormalizeBufferedResponseAlwaysDropsSignatures(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		status  int
		headers http.Header
		body    []byte
	}{
		{
			name:   "head removes noncanonical identity coding",
			method: http.MethodHead,
			status: http.StatusOK,
			headers: http.Header{
				"content-encoding": {"identity"},
				"Content-Length":   {"12"},
				"sIgNaTuRe":        {"sig"},
				"sIgNaTuRe-InPuT":  {`sig1=("content-encoding")`},
			},
		},
		{
			name:   "not modified removes identity coding",
			method: http.MethodGet,
			status: http.StatusNotModified,
			headers: http.Header{
				"Content-Encoding": {"identity"},
				"Content-Length":   {"12"},
				"Signature":        {"sig"},
				"Signature-Input":  {`sig1=("content-encoding")`},
			},
		},
		{
			name:   "no content deletes length",
			method: http.MethodGet,
			status: http.StatusNoContent,
			headers: http.Header{
				"content-length":  {"12"},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("content-length")`},
			},
		},
		{
			name:   "reset content deletes nonzero length",
			method: http.MethodGet,
			status: http.StatusResetContent,
			headers: http.Header{
				"Content-Length":  {"12"},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("content-length")`},
			},
		},
		{
			name:   "unchanged head framing removes signatures",
			method: http.MethodHead,
			status: http.StatusOK,
			headers: http.Header{
				"Content-Length":  {"12"},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("content-length")`},
			},
		},
		{
			name:   "unchanged not modified framing removes signatures",
			method: http.MethodGet,
			status: http.StatusNotModified,
			headers: http.Header{
				"Content-Length":  {"12"},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("content-length")`},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := normalizeBufferedResponse(test.method, test.status, test.headers, test.body)
			if len(headerFieldValues(got, "Signature")) != 0 || len(headerFieldValues(got, "Signature-Input")) != 0 {
				t.Fatalf("normalized headers retained signatures: %#v", got)
			}
		})
	}
}

func TestWriteBufferedResponseMethodAndStatusSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name              string
		method            string
		status            int
		headers           http.Header
		body              []byte
		wantBody          string
		wantContentLength string
	}{
		{name: "ordinary", method: http.MethodGet, status: http.StatusOK, headers: http.Header{"Content-Encoding": {"gzip"}}, body: []byte("plain"), wantBody: "plain", wantContentLength: "5"},
		{name: "head local reason", method: http.MethodHead, status: http.StatusBadGateway, body: []byte("reason"), wantContentLength: "6"},
		{name: "informational", method: http.MethodGet, status: http.StatusContinue, headers: http.Header{"Content-Length": {"7"}}, body: []byte("ignored")},
		{name: "no content", method: http.MethodGet, status: http.StatusNoContent, headers: http.Header{"Content-Length": {"7"}}, body: []byte("ignored")},
		{name: "reset content", method: http.MethodGet, status: http.StatusResetContent, headers: http.Header{"Content-Length": {"0"}}, body: []byte("ignored"), wantContentLength: "0"},
		{name: "not modified", method: http.MethodGet, status: http.StatusNotModified, headers: http.Header{"Content-Length": {"32"}}, body: []byte("ignored"), wantContentLength: "32"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(test.method, "http://gateway.test/", nil)
			handler := &Handler{writeTimeout: time.Second}
			if err := handler.writeBufferedResponse(context, test.status, test.headers, test.body); err != nil {
				t.Fatalf("writeBufferedResponse() error = %v", err)
			}
			if got := recorder.Body.String(); got != test.wantBody {
				t.Fatalf("body = %q, want %q", got, test.wantBody)
			}
			if got := recorder.Header().Get("Content-Length"); got != test.wantContentLength {
				t.Fatalf("Content-Length = %q, want %q", got, test.wantContentLength)
			}
		})
	}
}
