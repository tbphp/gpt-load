from pathlib import Path
import re


def replace_function(path: str, name: str, replacement: str) -> None:
    file_path = Path(path)
    text = file_path.read_text()
    match = re.search(rf"^func {re.escape(name)}\(", text, re.MULTILINE)
    if not match:
        raise RuntimeError(f"{path}: function {name} not found")
    next_match = re.search(r"^func ", text[match.start() + 1 :], re.MULTILINE)
    end = len(text) if next_match is None else match.start() + 1 + next_match.start()
    file_path.write_text(text[: match.start()] + replacement.rstrip() + "\n\n" + text[end:])


def replace_exact(path: str, old: str, new: str, count: int = 1) -> None:
    file_path = Path(path)
    text = file_path.read_text()
    actual = text.count(old)
    if actual < count:
        raise RuntimeError(f"{path}: expected at least {count} matches, found {actual}")
    file_path.write_text(text.replace(old, new, count))


# Decoder memory must have a practical codec floor while decoded output remains
# bounded by the exact caller limit.
replace_exact(
    "internal/platform/contentcoding/contentcoding.go",
    '''\t\tmemoryLimit := uint64(limit)
\t\tif memoryLimit == 0 {
\t\t\tmemoryLimit = 1
\t\t}
\t\twindowLimit := memoryLimit
\t\tif windowLimit < zstd.MinWindowSize {
\t\t\twindowLimit = zstd.MinWindowSize
\t\t}
''',
    '''\t\tmemoryLimit := uint64(limit)
\t\tconst minimumDecoderBudget = uint64(1 << 20)
\t\tif memoryLimit < minimumDecoderBudget {
\t\t\tmemoryLimit = minimumDecoderBudget
\t\t}
\t\twindowLimit := memoryLimit
''',
)
replace_exact(
    "internal/platform/contentcoding/contentcoding.go",
    '''\t\t\tif !valid {
\t\t\t\tcontinue
\t\t\t}
\t\t\tswitch name {
''',
    '''\t\t\tif !valid {
\t\t\t\tif name == string(EncodingIdentity) || name == "*" {
\t\t\t\t\treturn true
\t\t\t\t}
\t\t\t\tcontinue
\t\t\t}
\t\t\tswitch name {
''',
)
replace_exact(
    "internal/platform/contentcoding/contentcoding_test.go",
    '''\t\t{name: "malformed q is compatibility safe", values: []string{"identity;q=broken"}, want: true},
''',
    '''\t\t{name: "malformed q is compatibility safe", values: []string{"identity;q=broken"}, want: true},
\t\t{name: "malformed identity defeats wildcard rejection", values: []string{"identity;q=broken, *;q=0"}, want: true},
''',
)

# Group Header Rules must reject system-owned fields for remove as well as set.
replace_exact(
    "internal/state/runtime_settings.go",
    '''\t\t\tcanonicalName := textproto.CanonicalMIMEHeaderKey(name)
\t\t\tidentity := strings.ToLower(name)
''',
    '''\t\t\tcanonicalName := textproto.CanonicalMIMEHeaderKey(name)
\t\t\tif httpheader.IsForbiddenRequestRuleSetName(canonicalName) {
\t\t\t\treturn HeaderRules{}, fmt.Errorf(
\t\t\t\t\t"header_rules.remove cannot remove forbidden header %q",
\t\t\t\t\tcanonicalName,
\t\t\t\t)
\t\t\t}
\t\t\tidentity := strings.ToLower(name)
''',
    count=1,
)
replace_exact(
    "internal/state/runtime_settings_test.go",
    '''\t\t"pRoXy-Custom",
\t}
''',
    '''\t\t"pRoXy-Custom",
\t\t"Accept-Encoding",
\t\t"Content-Encoding",
\t\t"Content-Length",
\t}
''',
)
insert_after = '''func TestParseHeaderRulesRejectsUnsafeSetNames(t *testing.T) {'''
# Add a dedicated remove contract before duplicate identity tests.
replace_exact(
    "internal/state/runtime_settings_test.go",
    '''func TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities(t *testing.T) {
''',
    '''func TestParseHeaderRulesRejectsUnsafeRemoveNames(t *testing.T) {
\tfor _, name := range []string{"Accept-Encoding", "Content-Encoding", "Content-Length", "Connection"} {
\t\terr := ValidateRuntimeSetting(SettingHeaderRules, map[string]any{
\t\t\t"remove": []any{name},
\t\t})
\t\tif err == nil {
\t\t\tt.Errorf("parseHeaderRules accepted unsafe remove %q", name)
\t\t}
\t}
}

func TestValidateRuntimeSettingRejectsDuplicateHeaderRuleIdentities(t *testing.T) {
''',
)

# Request construction and discovery/probe paths share the same forbidden-rule
# filter instead of relying only on data-plane sanitization.
replace_exact(
    "internal/dialect/pipeline.go",
    '''import (
\t"net/http"
\t"strings"

\t"gpt-load/internal/state"
)
''',
    '''import (
\t"net/http"
\t"strings"

\t"gpt-load/internal/platform/httpheader"
\t"gpt-load/internal/state"
)
''',
)
replace_exact(
    "internal/dialect/pipeline.go",
    '''\tfor name, value := range rules.Set {
\t\theaders.Set(name, strings.ReplaceAll(value, "${API_KEY}", apiKey))
\t}
\tfor _, name := range rules.Remove {
\t\theaders.Del(name)
\t}
''',
    '''\tfor name, value := range rules.Set {
\t\tif httpheader.IsForbiddenRequestRuleSetName(name) {
\t\t\tcontinue
\t\t}
\t\theaders.Set(name, strings.ReplaceAll(value, "${API_KEY}", apiKey))
\t}
\tfor _, name := range rules.Remove {
\t\tif httpheader.IsForbiddenRequestRuleSetName(name) {
\t\t\tcontinue
\t\t}
\t\theaders.Del(name)
\t}
''',
)
replace_exact(
    "internal/dialect/pipeline_test.go",
    '''func TestApplyCredentialRemoveWinsOverSet(t *testing.T) {
''',
    '''func TestApplyCredentialIgnoresSystemOwnedContentCodingRules(t *testing.T) {
\theaders := http.Header{
\t\t"Accept-Encoding": {"identity"},
\t}
\trules := state.HeaderRules{
\t\tSet: map[string]string{
\t\t\t"Accept-Encoding":  "gzip",
\t\t\t"Content-Encoding": "zstd",
\t\t\t"Content-Length":   "1",
\t\t},
\t\tRemove: []string{"Accept-Encoding"},
\t}
\tApplyCredential(NewOpenAI(http.DefaultClient), headers, "sk-system", rules)
\tif got := headers.Get("Accept-Encoding"); got != "identity" {
\t\tt.Fatalf("Accept-Encoding = %q, want identity", got)
\t}
\tif headers.Get("Content-Encoding") != "" || headers.Get("Content-Length") != "" {
\t\tt.Fatalf("system-owned representation headers survived: %#v", headers)
\t}
}

func TestApplyCredentialRemoveWinsOverSet(t *testing.T) {
''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestForwardSuccessRepresentationPreservesUnchangedWireAndMetadata",
    r'''func TestForwardSuccessRepresentationReturnsPlainAndInvalidatesMetadata(t *testing.T) {
	plain := []byte(`{"id":"safe-response","usage":{"prompt_tokens":100,"completion_tokens":30}}`)
	for _, representation := range successRepresentationEncodings(t, plain) {
		t.Run(representation.name, func(t *testing.T) {
			result := forwardSuccessRepresentation(t, representation, "fake-provider-secret-unchanged", nil)
			if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
				t.Fatalf("Forward() result = %#v", result)
			}
			if !bytes.Equal(result.Body, plain) {
				t.Fatalf("downstream plain = %q, want %q", result.Body, plain)
			}
			if result.Header.Get("Content-Encoding") != "" ||
				result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
				t.Fatalf("plain framing headers = %#v", result.Header)
			}
			assertRepresentationMetadata(t, result.Header, false)
		})
	}
}''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestPrepareSuccessRepresentationRejectsUnchangedCredentialMetadataCollision",
    r'''func TestPrepareSuccessRepresentationDropsStaleCredentialMetadata(t *testing.T) {
	const credential = "fake-metadata-credential"
	wire := []byte(`{"id":"safe-response"}`)
	for _, name := range []string{
		"Content-Encoding", "ETag", "Digest", "Content-MD5", "Content-Range",
		"Content-Digest", "Repr-Digest", "Signature", "Signature-Input",
	} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{"Content-Encoding": {"identity"}}
			headers.Set(name, credential)
			input := ForwardInput{
				Dialect: dialect.NewOpenAI(http.DefaultClient),
				APIKey: credential,
				Request: &dialect.ParsedRequest{Header: make(http.Header)},
			}
			result, err := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).prepareSuccessRepresentation(
				input, headers, wire, []string{credential},
			)
			if err != nil || !bytes.Equal(result.wire, wire) || result.headers.Get("Content-Encoding") != "" ||
				result.headers.Get("Content-Length") != strconv.Itoa(len(wire)) {
				t.Fatalf("prepareSuccessRepresentation() = %#v, %v", result, err)
			}
			assertRepresentationMetadata(t, result.headers, false)
		})
	}

	contentLength := strconv.Itoa(len(wire))
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		APIKey: contentLength,
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}
	result, err := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).prepareSuccessRepresentation(
		input, make(http.Header), wire, []string{contentLength},
	)
	if !errors.Is(err, ErrUpstreamProtocol) || result.wire != nil || result.headers != nil {
		t.Fatalf("final Content-Length collision = %#v, %v", result, err)
	}
}''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestPrepareSuccessRepresentationRebuildsChangedBodyDespiteStaleCredentialMetadata",
    r'''func TestPrepareSuccessRepresentationReturnsChangedBodyAsPlain(t *testing.T) {
	const credential = "fake-stale-metadata-credential"
	plain := []byte(`{"echo":"fake-stale-metadata-credential"}`)
	wire, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{"Content-Encoding": {"gzip"}}
	setRepresentationMetadata(headers)
	result, err := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).prepareSuccessRepresentation(
		ForwardInput{Dialect: dialect.NewOpenAI(http.DefaultClient), APIKey: credential, Request: &dialect.ParsedRequest{Header: make(http.Header)}},
		headers, wire, []string{credential},
	)
	if err != nil || !result.changed || string(result.wire) != `{"echo":"[REDACTED]"}` {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v", result, err)
	}
	if result.headers.Get("Content-Encoding") != "" ||
		result.headers.Get("Content-Length") != strconv.Itoa(len(result.wire)) {
		t.Fatalf("plain framing headers = %#v", result.headers)
	}
	assertRepresentationMetadata(t, result.headers, false)
}''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestPrepareSuccessRepresentationRemovesAliasedModelFromUnchangedMetadata",
    r'''func TestPrepareSuccessRepresentationRemovesAliasedModelAndStaleMetadata(t *testing.T) {
	const upstreamModel = "provider-private-model"
	wire := []byte(`{"id":"safe-response"}`)
	headers := http.Header{"Content-Encoding": {"identity"}, "X-Safe": {"kept"}}
	for _, name := range []string{"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest", "Signature", "Signature-Input"} {
		headers.Set(name, "contains="+upstreamModel)
	}
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient), ExternalModel: "public-model",
		UpstreamModelID: upstreamModel, Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}
	result, err := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).prepareSuccessRepresentation(input, headers, wire, nil)
	if err != nil || result.changed || !bytes.Equal(result.wire, wire) {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v", result, err)
	}
	if result.headers.Get("Content-Encoding") != "" || result.headers.Get("Content-Length") != strconv.Itoa(len(wire)) ||
		result.headers.Get("X-Safe") != "kept" {
		t.Fatalf("safe headers = %#v", result.headers)
	}
	assertRepresentationMetadata(t, result.headers, false)
}''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestForwardSuccessRepresentationRedactsAndRebuildsMetadata",
    r'''func TestForwardSuccessRepresentationRedactsAndReturnsPlain(t *testing.T) {
	const apiKey = "fake-provider-secret-redaction"
	tests := []struct {
		name string
		plain []byte
		configureInput func(*ForwardInput)
		wantPlain []byte
	}{
		{name: "credential redaction", plain: []byte(`{"echo":"Bearer fake-provider-secret-redaction fake-provider-secret-redaction"}`), wantPlain: []byte(`{"echo":"[REDACTED] [REDACTED]"}`)},
		{name: "model alias rewrite", plain: []byte(`{"model":"provider-model","id":"safe-response"}`), configureInput: func(input *ForwardInput) {
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "provider-model"
		}, wantPlain: []byte(`{"id":"safe-response","model":"public-model"}`)},
	}
	for _, test := range tests {
		for _, representation := range successRepresentationEncodings(t, test.plain) {
			t.Run(test.name+"/"+representation.name, func(t *testing.T) {
				result := forwardSuccessRepresentation(t, representation, apiKey, test.configureInput)
				if result.Err != nil || result.StatusCode != http.StatusOK || !bytes.Equal(result.Body, test.wantPlain) {
					t.Fatalf("Forward() = %#v, want %q", result, test.wantPlain)
				}
				if result.Header.Get("Content-Encoding") != "" || result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
					t.Fatalf("plain framing headers = %#v", result.Header)
				}
				assertRepresentationMetadata(t, result.Header, false)
			})
		}
	}
}''',
)

replace_function(
    "internal/gateway/response_representation_test.go",
    "TestForwardSuccessRepresentationRejectsUnknownStackedMalformedAndPlainOverflow",
    r'''func TestForwardSuccessRepresentationRejectsUnsupportedMalformedAndOversizedBodies(t *testing.T) {
	compressedOverflow := gzipRepeatedByte(t, 'a', maxNonStreamingResponseBodyBytes+1)
	tests := []struct {
		name string
		encodings []string
		wire []byte
		writeBody func(io.Writer)
	}{
		{name: "unknown", encodings: []string{"compress"}, wire: []byte("opaque-wire")},
		{name: "stacked", encodings: []string{"gzip, br"}, wire: []byte("opaque-wire")},
		{name: "multiple fields", encodings: []string{"identity", "gzip"}, wire: []byte("opaque-wire")},
		{name: "malformed gzip", encodings: []string{"gzip"}, wire: []byte("not-a-gzip-stream")},
		{name: "malformed br", encodings: []string{"br"}, wire: []byte{0xff, 0xff, 0xff}},
		{name: "malformed deflate", encodings: []string{"deflate"}, wire: []byte("not-a-deflate-stream")},
		{name: "malformed zstd", encodings: []string{"zstd"}, wire: []byte("not-a-zstd-stream")},
		{name: "wire overflow", writeBody: func(writer io.Writer) { _, _ = io.CopyN(writer, repeatingByteReader('w'), maxNonStreamingResponseBodyBytes+1) }},
		{name: "plain overflow", encodings: []string{"gzip"}, wire: compressedOverflow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				for _, encoding := range test.encodings { writer.Header().Add("Content-Encoding", encoding) }
				if test.writeBody != nil { test.writeBody(writer) } else { _, _ = writer.Write(test.wire) }
			}))
			defer upstream.Close()
			result := testForward(t, upstream.URL, "fake-provider-secret", 10*time.Second)
			if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten || result.StatusCode != 0 || len(result.Body) != 0 {
				t.Fatalf("Forward() = %#v", result)
			}
		})
	}
}''',
)

replace_exact(
    "internal/gateway/response_representation_test.go",
    '''\tdownstreamPlain, err := utils.DecompressResponse("gzip", result.Body)
\tif err != nil {
\t\tt.Fatalf("decompress downstream response: %v", err)
\t}
''',
    '''\tdownstreamPlain := result.Body
''',
)
replace_exact(
    "internal/gateway/response_representation_test.go",
    '''\tif result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) ||
\t\t!strings.EqualFold(result.Header.Get("Content-Encoding"), "gzip") {
''',
    '''\tif result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) ||
\t\tresult.Header.Get("Content-Encoding") != "" {
''',
)

replace_function(
    "internal/gateway/forward_test.go",
    "TestForwarderBoundsDecompressedErrorBodies",
    r'''func TestForwarderBoundsDecompressedErrorBodies(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			for _, size := range []int{1 << 20, 1<<20 + 1} {
				plain := bytes.Repeat([]byte("x"), size)
				wire := compressResponseWithBoundedZstdWindow(t, encoding, plain)
				upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Encoding", encoding)
					writer.WriteHeader(http.StatusUnauthorized)
					_, _ = writer.Write(wire)
				}))
				result := testForward(t, upstream.URL, "key", 10*time.Second)
				upstream.Close()
				if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
					t.Fatalf("size %d result = %#v", size, result)
				}
				if size == 1<<20 {
					if !bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) || result.Header.Get("Content-Encoding") != "" ||
						result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
						t.Fatalf("exact limit result body lengths = %d/%d, headers=%#v", len(result.Body), len(result.ClassificationBody), result.Header)
					}
				} else if string(result.Body) != redact.Placeholder || string(result.ClassificationBody) != redact.Placeholder || result.Header.Get("Content-Encoding") != "" {
					t.Fatalf("overflow result = %#v", result)
				}
			}
		})
	}
}''',
)

replace_function(
    "internal/gateway/forward_test.go",
    "TestForwarderFailsClosedWhenRedactionExpandsErrorBeyondBounds",
    r'''func TestForwarderBoundsPlainErrorAfterRedaction(t *testing.T) {
	for _, test := range []struct {
		name string
		encoding string
		plain []byte
		wantPlaceholder bool
	}{
		{name: "identity expansion remains within decoded limit", plain: bytes.Repeat([]byte("a"), int(maxErrorResponseBodyBytes))},
		{name: "gzip expansion exceeds decoded limit", encoding: "gzip", plain: bytes.Repeat([]byte("a"), int(maxDecompressedErrorBodyBytes)), wantPlaceholder: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := test.plain
			if test.encoding != "" { var err error; wire, err = utils.CompressResponse(test.encoding, test.plain); if err != nil { t.Fatal(err) } }
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.encoding != "" { writer.Header().Set("Content-Encoding", test.encoding) }
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()
			result := testForward(t, upstream.URL, "a", time.Second)
			if result.Err != nil || result.StatusCode != http.StatusUnauthorized || result.Header.Get("Content-Encoding") != "" { t.Fatalf("result = %#v", result) }
			if test.wantPlaceholder {
				if string(result.Body) != redact.Placeholder || string(result.ClassificationBody) != redact.Placeholder { t.Fatalf("result = %#v", result) }
			} else if len(result.Body) <= int(maxErrorResponseBodyBytes) || !bytes.Equal(result.Body, result.ClassificationBody) || bytes.Contains(result.Body, []byte("a")) {
				t.Fatalf("expanded safe body = %d/%q", len(result.Body), result.Body[:min(len(result.Body), 32)])
			}
		})
	}
}''',
)

replace_function(
    "internal/gateway/forward_test.go",
    "TestForwarderFailsClosedWhenShortKeyMatchesContentEncoding",
    r'''func TestForwarderStripsEncodingWhenShortKeyMatchesCodingName(t *testing.T) {
	plain := []byte(`{"error":"safe"}`)
	wire, err := utils.CompressResponse("gzip", plain)
	if err != nil { t.Fatal(err) }
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(status)
				_, _ = w.Write(wire)
			}))
			defer upstream.Close()
			result := testForward(t, upstream.URL, "gzip", time.Second)
			if result.Err != nil || result.StatusCode != status || !bytes.Equal(result.Body, plain) || result.Header.Get("Content-Encoding") != "" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}''',
)

# All non-streaming error bodies are now plaintext downstream.
forward_path = Path("internal/gateway/forward_test.go")
forward_text = forward_path.read_text()
pattern = re.compile(r'''\n\t\t\tdownstreamBody := result\.Body\n\t\t\tif encoding != "" \{\n\t\t\t\tvar err error\n\t\t\t\tdownstreamBody, err = utils\.DecompressResponse\(encoding, result\.Body\)\n\t\t\t\tif err != nil \{\n\t\t\t\t\tt\.Fatalf\("decompress downstream body: %v", err\)\n\t\t\t\t\}\n\t\t\t\}\n''')
forward_text, replaced = pattern.subn('\n\t\t\tdownstreamBody := result.Body\n', forward_text)
if replaced < 3:
    raise RuntimeError(f"expected at least 3 compressed downstream blocks, replaced {replaced}")
forward_text = forward_text.replace('result.Header.Get("Content-Encoding") != encoding ||', 'result.Header.Get("Content-Encoding") != "" ||')
forward_path.write_text(forward_text)

replace_function(
    "internal/gateway/forward_test.go",
    "TestForwarderRedactsCompressedErrorAndPreservesEncoding",
    r'''func TestForwarderRedactsCompressedErrorAndReturnsPlain(t *testing.T) {
	const secret = "custom-upstream-secret"
	plain := []byte(`{"error":{"api_key":"` + secret + `","code":"invalid_api_key"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil { t.Fatal(err) }
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()
	result := testForward(t, upstream.URL, secret, time.Second)
	if result.Err != nil || result.StatusCode != http.StatusUnauthorized || result.Header.Get("Content-Encoding") != "" { t.Fatalf("Forward() = %#v", result) }
	for _, body := range [][]byte{result.Body, result.ClassificationBody} {
		if bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(redact.Placeholder)) { t.Fatalf("safe body = %q", body) }
	}
	if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) { t.Fatalf("headers = %#v", result.Header) }
	assertRepresentationMetadata(t, result.Header, false)
}''',
)
replace_function(
    "internal/gateway/forward_test.go",
    "TestForwarderPreservesUnchangedCompressedErrorWireBytes",
    r'''func TestForwarderReturnsUnchangedCompressedErrorAsPlain(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil { t.Fatal(err) }
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()
	result := testForward(t, upstream.URL, "custom-upstream-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusTooManyRequests || !bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) {
		t.Fatalf("Forward() = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" || result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) { t.Fatalf("headers = %#v", result.Header) }
	assertRepresentationMetadata(t, result.Header, false)
}''',
)

replace_function(
    "internal/gateway/forward_test.go",
    "TestForwardStreamInvalidatesRequestRepresentationMetadataOnlyWhenBodyChanges",
    r'''func TestForwardStreamAlwaysRebuildsPlainRequestRepresentationMetadata(t *testing.T) {
	type capturedRequest struct { body []byte; headers http.Header; contentLength int64 }
	for _, configure := range []func(*ForwardInput){
		nil,
		func(input *ForwardInput) { input.Group.InjectUsageOptions = true },
		func(input *ForwardInput) { input.ExternalModel = "public-model"; input.UpstreamModelID = "provider-model"; input.Request.Body = []byte(`{"model":"public-model","stream":true}`) },
		func(input *ForwardInput) { input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{"Digest": "sha-256=group-digest", "Signature": "group-signature", "Accept-Encoding": "gzip"}} },
	} {
		received := make(chan capturedRequest, 1)
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			received <- capturedRequest{body: body, headers: request.Header.Clone(), contentLength: request.ContentLength}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
		}))
		input := streamForwardInput(upstream.URL)
		setRepresentationMetadata(input.Request.Header)
		if configure != nil { configure(&input) }
		result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, newRecordingResponseWriter())
		upstream.Close()
		if result.Err != nil || !result.Committed { t.Fatalf("ForwardStream() = %#v", result) }
		got := <-received
		if got.contentLength != int64(len(got.body)) || got.headers.Get("Accept-Encoding") != "identity" { t.Fatalf("request = %#v", got) }
		assertRepresentationMetadata(t, got.headers, false)
	}
}''',
)

replace_exact(
    "internal/gateway/dialects_integration_test.go",
    '''\tif receivedHeader.Get("Accept-Encoding") != "gzip" {
\t\tt.Fatalf("upstream Accept-Encoding = %q, want HeaderRule gzip", receivedHeader.Get("Accept-Encoding"))
\t}
\tassertRepresentationMetadata(t, recorder.Header(), true)
''',
    '''\tif receivedHeader.Get("Accept-Encoding") != "identity" {
\t\tt.Fatalf("upstream Accept-Encoding = %q, want identity", receivedHeader.Get("Accept-Encoding"))
\t}
\tassertRepresentationMetadata(t, recorder.Header(), false)
''',
)
