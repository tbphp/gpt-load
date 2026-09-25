package geminiembedding

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
)

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestConvertRequestBuildsBatchEmbedContents(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		payload    string
		want       string
		conversion Conversion
	}{
		{
			name:       "single text defaults to float",
			payload:    `{"model":"public","input":"hello","user":"tenant"}`,
			want:       `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"hello"}]}}]}`,
			conversion: Conversion{InputCount: 1, UpstreamModel: "gemini-embedding-001"},
		},
		{
			name:       "text array with dimensions and base64",
			payload:    `{"model":"public","input":["a",""],"dimensions":768,"encoding_format":"base64"}`,
			want:       `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]},"outputDimensionality":768},{"model":"models/gemini-embedding-001","content":{"parts":[{"text":""}]},"outputDimensionality":768}]}`,
			conversion: Conversion{InputCount: 2, Base64: true, UpstreamModel: "gemini-embedding-001"},
		},
		{
			name:       "explicit float and null dimensions",
			payload:    `{"model":"public","input":["a"],"dimensions":null,"encoding_format":"float"}`,
			want:       `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]}}]}`,
			conversion: Conversion{InputCount: 1, UpstreamModel: "gemini-embedding-001"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body, conversion, err := ConvertRequest([]byte(test.payload), "gemini-embedding-001")
			if err != nil {
				t.Fatalf("ConvertRequest() error = %v", err)
			}
			assertJSON(t, body, test.want)
			if conversion != test.conversion {
				t.Fatalf("conversion = %#v, want %#v", conversion, test.conversion)
			}
		})
	}
}

func TestConvertRequestRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		payload     string
		unsupported bool
	}{
		{name: "token IDs", payload: `{"input":[1,2,3]}`, unsupported: true},
		{name: "token ID arrays", payload: `{"input":[[1,2],[3]]}`, unsupported: true},
		{name: "empty array", payload: `{"input":[]}`},
		{name: "missing input", payload: `{"model":"public"}`},
		{name: "non object", payload: `[]`},
		{name: "zero dimensions", payload: `{"input":"a","dimensions":0}`},
		{name: "fractional dimensions", payload: `{"input":"a","dimensions":1.5}`},
		{name: "unknown encoding", payload: `{"input":"a","encoding_format":"int8"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ConvertRequest([]byte(test.payload), "gemini-embedding-001")
			if err == nil {
				t.Fatal("ConvertRequest() error = nil")
			}
			var classified interface{ ConversionCode() string }
			isUnsupported := errors.As(err, &classified) &&
				classified.ConversionCode() == execution.ErrorCodeTargetConversionNotSupported
			if isUnsupported != test.unsupported {
				t.Fatalf("conversion unsupported = %t, want %t (%v)", isUnsupported, test.unsupported, err)
			}
		})
	}
}

func TestConvertResponseBuildsOpenAIEmbeddings(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"embeddings":[{"values":[0.1234567,-1,1e-7]},{"values":[0.5, 2]}],"usageMetadata":{"promptTokenCount":9,"totalTokenCount":9}}`)
	body, evidence, err := ConvertResponse(payload, Conversion{InputCount: 2, UpstreamModel: "gemini-embedding-001"})
	if err != nil {
		t.Fatalf("ConvertResponse() error = %v", err)
	}
	assertJSON(t, body, `{"object":"list","model":"gemini-embedding-001","data":[{"object":"embedding","index":0,"embedding":[0.1234567,-1,1e-7]},{"object":"embedding","index":1,"embedding":[0.5,2]}],"usage":{"prompt_tokens":9,"total_tokens":9}}`)
	if evidence == nil || evidence.Normalized.State != usage.StateComplete || evidence.Normalized.Tokens.UncachedInput != 9 {
		t.Fatalf("usage evidence = %#v", evidence)
	}
}

func TestConvertResponseEncodesBase64Float32LittleEndian(t *testing.T) {
	t.Parallel()

	body, _, err := ConvertResponse(
		[]byte(`{"embeddings":[{"values":[0.5,-2,1e-7]}]}`),
		Conversion{InputCount: 1, Base64: true, UpstreamModel: "m"},
	)
	if err != nil {
		t.Fatalf("ConvertResponse() error = %v", err)
	}
	var response struct {
		Data []struct {
			Embedding string `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || len(response.Data) != 1 {
		t.Fatalf("response = %s, err = %v", body, err)
	}
	raw, err := base64.StdEncoding.DecodeString(response.Data[0].Embedding)
	if err != nil || len(raw) != 12 {
		t.Fatalf("decoded base64 = %v, err = %v", raw, err)
	}
	for index, want := range []float32{0.5, -2, 1e-7} {
		if got := math.Float32frombits(binary.LittleEndian.Uint32(raw[index*4:])); got != want {
			t.Fatalf("value[%d] = %v, want %v", index, got, want)
		}
	}
}

func TestConvertResponseAlwaysReturnsUsageButKeepsMissingEvidence(t *testing.T) {
	t.Parallel()

	body, evidence, err := ConvertResponse(
		[]byte(`{"embeddings":[{"values":[0.1]}]}`),
		Conversion{InputCount: 1, UpstreamModel: "m"},
	)
	if err != nil {
		t.Fatalf("ConvertResponse() error = %v", err)
	}
	assertJSON(t, body, `{"object":"list","model":"m","data":[{"object":"embedding","index":0,"embedding":[0.1]}],"usage":{"prompt_tokens":0,"total_tokens":0}}`)
	if evidence == nil || evidence.Normalized.State != usage.StateMissing {
		t.Fatalf("usage evidence = %#v, want missing", evidence)
	}
}

func TestConvertResponseRejectsInvalidVectors(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`[]`,
		`{"embeddings":[]}`,
		`{"embeddings":[{"values":[0.1]},{"values":[0.2]}]}`,
		`{"embeddings":[{"values":[]}]}`,
		`{"embeddings":[{"values":[null]}]}`,
		`{"embeddings":[{"values":["0.1"]}]}`,
		`{"embeddings":[{"values":[[0.1]]}]}`,
		`{"embeddings":[{}]}`,
		`{"embeddings":[{"values":{"0":0.1}}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			if _, _, err := ConvertResponse([]byte(payload), Conversion{InputCount: 1, UpstreamModel: "m"}); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("ConvertResponse() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
	if _, _, err := ConvertResponse(
		[]byte(`{"embeddings":[{"values":[1e39]}]}`),
		Conversion{InputCount: 1, Base64: true, UpstreamModel: "m"},
	); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("float32 overflow error = %v, want ErrInvalidResponse", err)
	}
}
