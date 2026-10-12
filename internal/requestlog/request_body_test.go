package requestlog

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

var truncatedStringPattern = regexp.MustCompile(`(?s)^(.*)…\[truncated (\d+) bytes\]$`)

func TestRecordRequestBodyKeepsSmallJSONVerbatim(t *testing.T) {
	raw := []byte("{\"model\":\"gpt-x\",  \"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\n\"n\":1.50}")
	state, body := recordRequestBody(raw)
	if state != RequestBodyOriginal || !bytes.Equal(body, raw) {
		t.Fatalf("state = %q, body = %s", state, body)
	}
}

func TestRecordRequestBodyKeepsBodyAtLimitVerbatim(t *testing.T) {
	prefix, suffix := `{"content":"`, `"}`
	raw := []byte(prefix + strings.Repeat("a", maxRecordedRequestBodyBytes-len(prefix)-len(suffix)) + suffix)
	if len(raw) != maxRecordedRequestBodyBytes {
		t.Fatalf("fixture size = %d", len(raw))
	}
	state, body := recordRequestBody(raw)
	if state != RequestBodyOriginal || !bytes.Equal(body, raw) {
		t.Fatalf("state = %q, body size = %d", state, len(body))
	}
}

func TestRecordRequestBodyRejectsNonJSON(t *testing.T) {
	for name, raw := range map[string][]byte{
		"multipart":    []byte("--boundary\r\nContent-Disposition: form-data; name=\"image\"\r\n\r\nabc\r\n--boundary--"),
		"incomplete":   []byte(`{"model":`),
		"invalid_utf8": append([]byte(`{"content":"`), 0xff, '"', '}'),
	} {
		t.Run(name, func(t *testing.T) {
			state, body := recordRequestBody(raw)
			if state != RequestBodyNonJSON || body != nil {
				t.Fatalf("state = %q, body = %q", state, body)
			}
		})
	}
}

func TestRecordRequestBodyTruncatesOnlyStringValues(t *testing.T) {
	long := strings.Repeat("a", 300<<10)
	longKey := strings.Repeat("k", 3<<10)
	raw := []byte("{\n  \"z\": 1,\n  \"model\": \"gpt-x\",\n  \"messages\": [{\"role\": \"user\", \"content\": " +
		quoteJSON(t, long) + "}, {\"role\": \"assistant\", \"content\": \"ok\"}],\n  " +
		quoteJSON(t, longKey) + ": true,\n  \"temperature\": 0.70,\n  \"stop\": null,\n  \"a\": [1, 2e3, -3]\n}")

	state, body := recordRequestBody(raw)
	if state != RequestBodyTruncated {
		t.Fatalf("state = %q", state)
	}
	if len(body) > maxRecordedRequestBodyBytes || !json.Valid(body) {
		t.Fatalf("recorded body size = %d, valid = %v", len(body), json.Valid(body))
	}
	if !bytes.HasPrefix(body, []byte("{\n  \"z\": 1,\n  \"model\": \"gpt-x\",")) {
		t.Fatalf("formatting outside strings changed: %.80s", body)
	}
	if !bytes.Contains(body, []byte(longKey)) {
		t.Fatal("object keys must never be truncated")
	}
	truncated := assertSameStructure(t, raw, body)
	if len(truncated) != 1 {
		t.Fatalf("truncated strings = %d, want 1", len(truncated))
	}
	if truncated[0].prefix != long[:initialRequestBodyStringBytes] ||
		truncated[0].omitted != len(long)-initialRequestBodyStringBytes {
		t.Fatalf("truncated prefix = %d bytes, omitted = %d", len(truncated[0].prefix), truncated[0].omitted)
	}
}

func TestRecordRequestBodyLowersStringLimitUntilBodyFits(t *testing.T) {
	values := make([]string, 300)
	for index := range values {
		values[index] = quoteJSON(t, strings.Repeat(string(rune('a'+index%26)), 1500))
	}
	raw := []byte(`{"input":[` + strings.Join(values, ",") + `]}`)

	state, body := recordRequestBody(raw)
	if state != RequestBodyTruncated || len(body) > maxRecordedRequestBodyBytes {
		t.Fatalf("state = %q, size = %d", state, len(body))
	}
	truncated := assertSameStructure(t, raw, body)
	if len(truncated) != len(values) {
		t.Fatalf("truncated strings = %d, want %d", len(truncated), len(values))
	}
	for _, value := range truncated {
		if len(value.prefix) != 512 || value.omitted != 1500-512 {
			t.Fatalf("prefix = %d bytes, omitted = %d; want the 512-byte level", len(value.prefix), value.omitted)
		}
	}
}

func TestRecordRequestBodyNeverSplitsCharactersOrEscapes(t *testing.T) {
	// 运行时拼出 JSON 的 \u 转义，避免源码里的转义被提前解释成字符。
	escape := func(hex string) string { return string(rune(92)) + "u" + hex }
	for name, content := range map[string]string{
		"multibyte": quoteJSON(t, strings.Repeat("界", 100<<10)),
		"escapes":   `"` + strings.Repeat(`line\n\"quoted\"\\`, 30<<10) + `"`,
		"unicode":   `"` + strings.Repeat(escape("00e9"), 60<<10) + `"`,
		"surrogate": `"` + strings.Repeat(escape("d83d")+escape("de00"), 30<<10) + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"content":` + content + `}`)
			state, body := recordRequestBody(raw)
			if state != RequestBodyTruncated || len(body) > maxRecordedRequestBodyBytes {
				t.Fatalf("state = %q, size = %d", state, len(body))
			}
			truncated := assertSameStructure(t, raw, body)
			if len(truncated) != 1 {
				t.Fatalf("truncated strings = %d", len(truncated))
			}
			prefix := truncated[0].prefix
			if prefix == "" || !utf8.ValidString(prefix) || strings.ContainsRune(prefix, utf8.RuneError) {
				t.Fatalf("prefix is not clean text: %q", prefix[:min(len(prefix), 32)])
			}
			if name == "multibyte" && len(prefix) != 2046 {
				t.Fatalf("multibyte prefix = %d bytes, want 2046", len(prefix))
			}
		})
	}
}

func TestRecordRequestBodyReportsStructureThatCannotFit(t *testing.T) {
	numbers := make([]string, 120<<10)
	for index := range numbers {
		numbers[index] = strconv.Itoa(100000 + index)
	}
	raw := []byte(`{"input":[` + strings.Join(numbers, ",") + `]}`)

	state, body := recordRequestBody(raw)
	if state != RequestBodyTooLarge || body != nil {
		t.Fatalf("state = %q, body size = %d", state, len(body))
	}
}

type truncatedRequestString struct {
	prefix  string
	omitted int
}

type requestBodyToken struct {
	value any
	key   bool
}

// assertSameStructure 要求两份 JSON 的键、顺序、数组长度和非字符串值完全一致，
// 字符串值只能原样保留或保留开头并带截断标记。
func assertSameStructure(t *testing.T, original, recorded []byte) []truncatedRequestString {
	t.Helper()
	before, after := requestBodyTokens(t, original), requestBodyTokens(t, recorded)
	if len(before) != len(after) {
		t.Fatalf("token count = %d, want %d", len(after), len(before))
	}
	var truncated []truncatedRequestString
	for index := range before {
		want, got := before[index], after[index]
		if want.key != got.key {
			t.Fatalf("token %d key flag = %v, want %v", index, got.key, want.key)
		}
		wantText, isString := want.value.(string)
		if !isString || want.key || got.value == want.value {
			if got.value != want.value {
				t.Fatalf("token %d = %#v, want %#v", index, got.value, want.value)
			}
			continue
		}
		gotText, _ := got.value.(string)
		match := truncatedStringPattern.FindStringSubmatch(gotText)
		if match == nil || !strings.HasPrefix(wantText, match[1]) {
			t.Fatalf("token %d is not a marked prefix of the original string", index)
		}
		omitted, err := strconv.Atoi(match[2])
		if err != nil || omitted <= 0 {
			t.Fatalf("token %d omitted bytes = %q", index, match[2])
		}
		truncated = append(truncated, truncatedRequestString{prefix: match[1], omitted: omitted})
	}
	return truncated
}

func requestBodyTokens(t *testing.T, raw []byte) []requestBodyToken {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	type frame struct{ object, expectKey bool }
	var stack []frame
	var tokens []requestBodyToken
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return tokens
		}
		if err != nil {
			t.Fatal(err)
		}
		delim, isDelim := token.(json.Delim)
		key := false
		if len(stack) > 0 && stack[len(stack)-1].object && !(isDelim && delim == '}') {
			top := &stack[len(stack)-1]
			key, top.expectKey = top.expectKey, !top.expectKey
		}
		if isDelim {
			switch delim {
			case '{':
				stack = append(stack, frame{object: true, expectKey: true})
			case '[':
				stack = append(stack, frame{})
			default:
				stack = stack[:len(stack)-1]
			}
		}
		tokens = append(tokens, requestBodyToken{value: token, key: key})
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
