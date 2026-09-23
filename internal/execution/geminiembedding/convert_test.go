package geminiembedding

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/execution"
)

func TestConvertRequestSuccess(t *testing.T) {
	dim := 512
	body100, _ := json.Marshal(map[string]any{"model": "text-embedding-004", "input": make([]string, 100)})

	tests := []struct {
		name          string
		payload       string
		upstreamModel string
		wantTexts     int
		wantDim       *int
		wantFmt       string
		wantModel     string
	}{
		{
			name:          "single text default format",
			payload:       `{"model":"text-embedding-004","input":"hello world"}`,
			upstreamModel: "text-embedding-004",
			wantTexts:     1,
			wantFmt:       "float",
			wantModel:     "text-embedding-004",
		},
		{
			name:          "text array with dimensions and base64",
			payload:       `{"model":"text-embedding-004","input":["first","second"],"dimensions":512,"encoding_format":"base64"}`,
			upstreamModel: "models/text-embedding-004",
			wantTexts:     2,
			wantDim:       &dim,
			wantFmt:       "base64",
			wantModel:     "text-embedding-004",
		},
		{
			name:          "batch 100 texts",
			payload:       string(body100),
			upstreamModel: "text-embedding-004",
			wantTexts:     100,
			wantFmt:       "float",
			wantModel:     "text-embedding-004",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBody, contract, err := ConvertRequest([]byte(tt.payload), tt.upstreamModel)
			if err != nil {
				t.Fatalf("ConvertRequest unexpected error: %v", err)
			}
			if contract.InputCount != tt.wantTexts {
				t.Errorf("InputCount = %d, want %d", contract.InputCount, tt.wantTexts)
			}
			if !reflect.DeepEqual(contract.RequestedDimensions, tt.wantDim) {
				t.Errorf("RequestedDimensions = %v, want %v", contract.RequestedDimensions, tt.wantDim)
			}
			if contract.EncodingFormat != tt.wantFmt {
				t.Errorf("EncodingFormat = %q, want %q", contract.EncodingFormat, tt.wantFmt)
			}
			if contract.CanonicalModel != tt.wantModel {
				t.Errorf("CanonicalModel = %q, want %q", contract.CanonicalModel, tt.wantModel)
			}

			// Validate generated Gemini batch body
			var parsed struct {
				Requests []struct {
					Model   string `json:"model"`
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
					OutputDimensionality *int `json:"outputDimensionality"`
				} `json:"requests"`
			}
			if err := json.Unmarshal(gotBody, &parsed); err != nil {
				t.Fatalf("generated Gemini body invalid JSON: %v", err)
			}
			if len(parsed.Requests) != tt.wantTexts {
				t.Fatalf("requests len = %d, want %d", len(parsed.Requests), tt.wantTexts)
			}
			for i, r := range parsed.Requests {
				if r.Model != "models/text-embedding-004" {
					t.Errorf("request[%d].Model = %q, want models/text-embedding-004", i, r.Model)
				}
				if !reflect.DeepEqual(r.OutputDimensionality, tt.wantDim) {
					t.Errorf("request[%d].OutputDimensionality = %v, want %v", i, r.OutputDimensionality, tt.wantDim)
				}
			}
		})
	}
}

func TestConvertRequestValidationErrors(t *testing.T) {
	body101, _ := json.Marshal(map[string]any{"model": "test", "input": make([]string, 101)})

	tests := []struct {
		name        string
		payload     string
		isFallback  bool // Whether it should return ErrorCodeTargetConversionNotSupported
		errContains string
	}{
		{
			name:        "empty payload",
			payload:     `{}`,
			errContains: "Embeddings input is required",
		},
		{
			name:        "token array input",
			payload:     `{"model":"test","input":[102, 203]}`,
			isFallback:  true,
			errContains: "token IDs",
		},
		{
			name:        "batch limit 101",
			payload:     string(body101),
			isFallback:  true,
			errContains: "limit exceeded",
		},
		{
			name:        "invalid dimensions string",
			payload:     `{"model":"test","input":"hello","dimensions":"768"}`,
			errContains: "dimensions must be a positive integer",
		},
		{
			name:        "invalid dimensions negative",
			payload:     `{"model":"test","input":"hello","dimensions":-5}`,
			errContains: "dimensions must be a positive integer",
		},
		{
			name:        "invalid encoding_format",
			payload:     `{"model":"test","input":"hello","encoding_format":"int"}`,
			errContains: "encoding_format must be 'float' or 'base64'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ConvertRequest([]byte(tt.payload), "text-embedding-004")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errContains)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
			}
			var classified interface{ ConversionCode() string }
			if tt.isFallback {
				if !errors.As(err, &classified) || classified.ConversionCode() != execution.ErrorCodeTargetConversionNotSupported {
					t.Errorf("error should have ConversionCode %q", execution.ErrorCodeTargetConversionNotSupported)
				}
			}
		})
	}
}

func TestConvertResponse(t *testing.T) {
	dim := 3
	t.Run("float fidelity", func(t *testing.T) {
		const precise = "[0.12345678901234567,-0.00000000000000001234,42.0]"
		body, usage, err := ConvertResponse([]byte(fmt.Sprintf(`{"embeddings":[{"values":%s}],"usageMetadata":{"promptTokenCount":5}}`, precise)), ResponseContract{InputCount: 1, EncodingFormat: "float", CanonicalModel: "text-embedding-004"})
		if err != nil || usage == nil || usage.Normalized.Tokens.UncachedInput != 5 || !bytes.Contains(body, []byte(precise)) {
			t.Fatalf("unexpected result: body=%s, usage=%#v, err=%v", body, usage, err)
		}
	})
	t.Run("float opaque large numbers", func(t *testing.T) {
		const largeNumber = "[1e400]"
		body, _, err := ConvertResponse([]byte(fmt.Sprintf(`{"embeddings":[{"values":%s}]}`, largeNumber)), ResponseContract{InputCount: 1, EncodingFormat: "float", CanonicalModel: "text-embedding-004"})
		if err != nil || !bytes.Contains(body, []byte(largeNumber)) {
			t.Fatalf("expected opaque retention of large number without float64 parse failure: %v", err)
		}
	})
	t.Run("base64 format", func(t *testing.T) {
		body, _, err := ConvertResponse([]byte(`{"embeddings":[{"values":[1.0,-2.0,3.5]}]}`), ResponseContract{InputCount: 1, RequestedDimensions: &dim, EncodingFormat: "base64", CanonicalModel: "text-embedding-004"})
		if err != nil {
			t.Fatal(err)
		}
		var res struct{ Data []struct{ Embedding string } }
		if err := json.Unmarshal(body, &res); err != nil || len(res.Data) != 1 {
			t.Fatalf("unmarshal: %v", err)
		}
		b, _ := base64.StdEncoding.DecodeString(res.Data[0].Embedding)
		if len(b) != 12 || math.Float32frombits(binary.LittleEndian.Uint32(b[:4])) != 1.0 {
			t.Fatalf("unexpected base64 decoded data: %v", b)
		}
	})
	t.Run("base64 overflow rejects", func(t *testing.T) {
		for _, overflowJSON := range []string{`{"embeddings":[{"values":[1e40]}]}`, `{"embeddings":[{"values":[-1e40]}]}`} {
			_, _, err := ConvertResponse([]byte(overflowJSON), ResponseContract{InputCount: 1, EncodingFormat: "base64", CanonicalModel: "text-embedding-004"})
			if err == nil {
				t.Fatalf("expected overflow error for %s, got nil", overflowJSON)
			}
		}
	})
}

func TestConvertResponseStrictValidation(t *testing.T) {
	reqDim := 3
	contract := ResponseContract{
		InputCount:          2,
		RequestedDimensions: &reqDim,
		EncodingFormat:      "float",
		CanonicalModel:      "text-embedding-004",
	}

	tests := []struct {
		name        string
		respJSON    string
		errContains string
	}{
		{
			name:        "count mismatch (1 instead of 2)",
			respJSON:    `{"embeddings":[{"values":[1, 2, 3]}]}`,
			errContains: "count mismatch",
		},
		{
			name:        "null inside vector (must reject, not convert to 0)",
			respJSON:    `{"embeddings":[{"values":[1, null, 3]},{"values":[1, 2, 3]}]}`,
			errContains: "element must be a valid number",
		},
		{
			name:        "boolean inside vector",
			respJSON:    `{"embeddings":[{"values":[1, true, 3]},{"values":[1, 2, 3]}]}`,
			errContains: "element must be a valid number",
		},
		{
			name:        "empty vector",
			respJSON:    `{"embeddings":[{"values":[]},{"values":[]}]}`,
			errContains: "vector must not be empty",
		},
		{
			name:        "batch dimension inconsistent",
			respJSON:    `{"embeddings":[{"values":[1, 2, 3]},{"values":[1, 2]}]}`,
			errContains: "batch dimension inconsistent",
		},
		{
			name:        "requested dimension mismatch",
			respJSON:    `{"embeddings":[{"values":[1, 2]},{"values":[1, 2]}]}`,
			errContains: "dimension mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ConvertResponse([]byte(tt.respJSON), contract)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errContains)
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
			}
		})
	}
}
