package geminiembedding

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/usage"
)

var ErrInvalidResponse = errors.New("Gemini embedding response does not conform to contract")

type ResponseContract struct {
	InputCount          int
	RequestedDimensions *int
	EncodingFormat      string // "float" or "base64"
	CanonicalModel      string
}

type unsupportedRequestError string

func (e unsupportedRequestError) Error() string { return string(e) }
func (e unsupportedRequestError) ConversionCode() string {
	return execution.ErrorCodeTargetConversionNotSupported
}

// ConvertRequest validates and converts an OpenAI Embeddings request payload into a Gemini batchEmbedContents request.
func ConvertRequest(payload []byte, upstreamModel string) ([]byte, ResponseContract, error) {
	var req struct {
		Input          json.RawMessage `json:"input"`
		Dimensions     json.RawMessage `json:"dimensions"`
		EncodingFormat json.RawMessage `json:"encoding_format"`
	}
	if err := json.Unmarshal(payload, &req); err != nil || len(payload) == 0 || bytes.TrimSpace(payload)[0] != '{' {
		return nil, ResponseContract{}, errors.New("Embeddings request must be a JSON object")
	}

	// 1. Parse input
	rawInput := bytes.TrimSpace(req.Input)
	if len(rawInput) == 0 {
		return nil, ResponseContract{}, errors.New("Embeddings input is required")
	}

	var texts []string
	if rawInput[0] == '"' {
		var single string
		if err := json.Unmarshal(rawInput, &single); err != nil {
			return nil, ResponseContract{}, errors.New("decode text input")
		}
		texts = []string{single}
	} else if rawInput[0] == '[' {
		var rawArray []json.RawMessage
		if err := json.Unmarshal(rawInput, &rawArray); err != nil || len(rawArray) == 0 {
			return nil, ResponseContract{}, errors.New("decode array input")
		}
		first := bytes.TrimSpace(rawArray[0])
		if len(first) > 0 && first[0] != '"' {
			return nil, ResponseContract{}, unsupportedRequestError("Gemini embedding does not support token IDs")
		}
		texts = make([]string, len(rawArray))
		for i, rawItem := range rawArray {
			if err := json.Unmarshal(rawItem, &texts[i]); err != nil {
				return nil, ResponseContract{}, errors.New("decode text array element")
			}
		}
	} else {
		return nil, ResponseContract{}, errors.New("unsupported Embeddings input shape")
	}

	// 2. Validate batch limit (Gemini batchEmbedContents max 100 requests)
	if len(texts) > 100 {
		return nil, ResponseContract{}, unsupportedRequestError(
			fmt.Sprintf("Gemini batchEmbedContents limit exceeded (requested %d, max 100)", len(texts)),
		)
	}

	// 3. Parse dimensions strictly
	var requestedDimensions *int
	if len(req.Dimensions) > 0 && !bytes.Equal(bytes.TrimSpace(req.Dimensions), []byte("null")) {
		var dim int
		if err := json.Unmarshal(req.Dimensions, &dim); err != nil || dim <= 0 {
			return nil, ResponseContract{}, errors.New("dimensions must be a positive integer")
		}
		requestedDimensions = &dim
	}

	// 4. Parse encoding_format
	encodingFormat := "float"
	if len(req.EncodingFormat) > 0 && !bytes.Equal(bytes.TrimSpace(req.EncodingFormat), []byte("null")) {
		var fmtStr string
		if err := json.Unmarshal(req.EncodingFormat, &fmtStr); err != nil {
			return nil, ResponseContract{}, errors.New("encoding_format must be a string")
		}
		if fmtStr != "float" && fmtStr != "base64" {
			return nil, ResponseContract{}, errors.New("encoding_format must be 'float' or 'base64'")
		}
		encodingFormat = fmtStr
	}

	// 5. Canonicalize model
	cleanModel := strings.TrimPrefix(upstreamModel, "models/")
	modelIdentifier := "models/" + cleanModel

	requests := make([]map[string]any, len(texts))
	for i, text := range texts {
		reqItem := map[string]any{
			"model": modelIdentifier,
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		}
		if requestedDimensions != nil {
			reqItem["outputDimensionality"] = *requestedDimensions
		}
		requests[i] = reqItem
	}

	convertedBody, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		return nil, ResponseContract{}, err
	}

	contract := ResponseContract{
		InputCount:          len(texts),
		RequestedDimensions: requestedDimensions,
		EncodingFormat:      encodingFormat,
		CanonicalModel:      cleanModel,
	}
	return convertedBody, contract, nil
}

// ConvertResponse converts a Gemini batchEmbedContents response into a standard OpenAI Embeddings response.
func ConvertResponse(payload []byte, contract ResponseContract) ([]byte, *execution.UsageEvidence, error) {
	var geminiResp struct {
		Embeddings []struct {
			Values json.RawMessage `json:"values"`
		} `json:"embeddings"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
	}
	if err := json.Unmarshal(payload, &geminiResp); err != nil {
		return nil, nil, ErrInvalidResponse
	}

	// 1. Strict count check
	if len(geminiResp.Embeddings) != contract.InputCount {
		return nil, nil, fmt.Errorf("%w: count mismatch (expected %d, got %d)",
			ErrInvalidResponse, contract.InputCount, len(geminiResp.Embeddings))
	}

	type openAIItem struct {
		Object    string `json:"object"`
		Index     int    `json:"index"`
		Embedding any    `json:"embedding"` // json.RawMessage or string (base64)
	}

	data := make([]openAIItem, contract.InputCount)
	expectedDim := -1

	for i, emb := range geminiResp.Embeddings {
		// 2. Strict scalar number check (blocks null, boolean, nested arrays)
		dim, err := validateVectorDimensions(emb.Values)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: item %d invalid: %w", ErrInvalidResponse, i, err)
		}

		// 3. Batch dimension consistency check
		if i == 0 {
			expectedDim = dim
			if contract.RequestedDimensions != nil && expectedDim != *contract.RequestedDimensions {
				return nil, nil, fmt.Errorf("%w: dimension mismatch (expected %d, got %d)",
					ErrInvalidResponse, *contract.RequestedDimensions, expectedDim)
			}
		} else if dim != expectedDim {
			return nil, nil, fmt.Errorf("%w: batch dimension inconsistent (item %d got %d, first %d)",
				ErrInvalidResponse, i, dim, expectedDim)
		}

		var embVal any = emb.Values
		if contract.EncodingFormat == "base64" {
			b64Str, err := encodeBase64Vector(emb.Values, dim)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: item %d base64 encoding failed: %w", ErrInvalidResponse, i, err)
			}
			embVal = b64Str
		}
		data[i] = openAIItem{
			Object:    "embedding",
			Index:     i,
			Embedding: embVal,
		}
	}

	resMap := map[string]any{
		"object": "list",
		"data":   data,
		"model":  contract.CanonicalModel,
	}

	// 5. Usage extraction via dialect
	var usageEvidence *execution.UsageEvidence
	if len(geminiResp.UsageMetadata) > 0 && !bytes.Equal(bytes.TrimSpace(geminiResp.UsageMetadata), []byte("null")) {
		normalized, err := dialect.NewGemini().ExtractUsage(payload)
		if err == nil && normalized.State != usage.StateMissing && normalized.Diagnostics == (usage.Diagnostics{}) {
			usageEvidence = &execution.UsageEvidence{
				Normalized: normalized,
				Raw:        bytes.Clone(geminiResp.UsageMetadata),
			}
			tokens := normalized.Tokens.UncachedInput + normalized.Tokens.CacheRead
			resMap["usage"] = map[string]any{
				"prompt_tokens": tokens,
				"total_tokens":  tokens,
			}
		}
	}

	out, err := json.Marshal(resMap)
	if err != nil {
		return nil, nil, err
	}
	return out, usageEvidence, nil
}

// validateVectorDimensions strictly checks a 1D numeric array without allocating a float slice.
func validateVectorDimensions(raw []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return 0, errors.New("must start with [")
	}

	dim := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, err
		}
		if _, ok := tok.(json.Number); !ok {
			return 0, fmt.Errorf("element must be a valid number, got %T", tok)
		}
		dim++
	}

	tok, err = dec.Token()
	if err != nil || tok != json.Delim(']') {
		return 0, errors.New("must end with ]")
	}
	if dim == 0 {
		return 0, errors.New("vector must not be empty")
	}
	return dim, nil
}

func encodeBase64Vector(raw []byte, dim int) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	_, _ = dec.Token() // skip '['

	buf := make([]byte, dim*4)
	idx := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		num := tok.(json.Number)
		f64, err := strconv.ParseFloat(string(num), 32)
		if err != nil || math.IsInf(f64, 0) || math.IsNaN(f64) {
			return "", fmt.Errorf("float32 overflow or invalid number: %s", num)
		}
		binary.LittleEndian.PutUint32(buf[idx*4:], math.Float32bits(float32(f64)))
		idx++
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}
