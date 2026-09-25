package requestredact

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/platform/encryption"
)

func signedTestCipher(t *testing.T, accessKeyID uint) encryption.RedactionCipher {
	t.Helper()
	service, err := encryption.NewService("synthetic-signed-content-master-key-2026-09-25")
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := service.NewRedactionCipher(accessKeyID)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func signedTestRules(t *testing.T) map[string]*Compiled {
	t.Helper()
	rules := map[string]*Compiled{}
	for name, list := range map[string][]Rule{
		"encrypt": {{Pattern: `[a-z]+@example\.com`, Mode: ModeEncrypt}},
		"replace": {{Pattern: `[a-z]+@example\.com`, Replacement: "[EMAIL]"}},
		"none":    nil,
	} {
		compiled, err := Compile(list)
		if err != nil {
			t.Fatal(err)
		}
		rules[name] = compiled
	}
	return rules
}

func signedTestString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestSignatureRecordRoundTrip(t *testing.T) {
	values := []SignedString{
		{Path: []any{"thinking"}, Value: "user alice", Spans: []RestoreSpan{{Offset: 5, Length: 5, Original: "gld1_44_a"}}},
		{Path: []any{"functionCall", "args", "to", 0}, Value: "bob"},
	}
	wrapped := WrapSignature("SIG123", values)
	if !strings.HasPrefix(wrapped, signatureWrapperPrefix) {
		t.Fatalf("wrapped signature %q lacks the fixed prefix", wrapped)
	}
	raw, err := base64.StdEncoding.DecodeString(wrapped)
	if err != nil {
		t.Fatalf("wrapped signature is not valid base64: %v", err)
	}
	// 客户端可能按字节解码后用 URL 安全字母表、无填充重新编码。
	for _, value := range []string{wrapped, base64.RawURLEncoding.EncodeToString(raw)} {
		record, ok := unwrapSignature(value)
		if !ok || record == nil || record.Signature != "SIG123" || len(record.Strings) != 2 {
			t.Fatalf("unwrapSignature(%q) = %#v, %v", value, record, ok)
		}
		first, second := record.Strings[0], record.Strings[1]
		if first.Path[0] != "thinking" || first.Hash != signatureHash("user alice") ||
			len(first.Spans) != 1 || first.Spans[0] != (signatureSpan{Offset: 5, Length: 5, Original: "gld1_44_a"}) ||
			second.Path[3] != 0 || len(second.Spans) != 0 {
			t.Fatalf("record entries = %#v", record.Strings)
		}
	}
	// 没有字符串的签名块也要包装：签名之后才到的内容会让核对失败，从而按普通内容加密。
	if record, ok := unwrapSignature(WrapSignature("SIG123", nil)); !ok || record == nil ||
		record.Signature != "SIG123" || len(record.Strings) != 0 {
		t.Fatalf("empty record = %#v, %v", record, ok)
	}
	for _, value := range []string{"SIG123", "EqQBCkgIARABGAIiQL"} {
		if _, ok := unwrapSignature(value); ok {
			t.Fatalf("ordinary signature %q was treated as a record", value)
		}
	}
	if record, ok := unwrapSignature(signatureWrapperPrefix + "!!"); !ok || record != nil {
		t.Fatalf("damaged wrapper = %#v, %v; want wrapped without record", record, ok)
	}
}

func TestApplyLeavesUnrecordedSignedContentUnchanged(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	signed := []string{
		`{"type":"thinking","thinking":"test with bob@example.com","signature":"SIG"}`,
		`{"functionCall":{"name":"lookup_user","args":{"email":"bob@example.com"}},"thoughtSignature":"SIG"}`,
		`{"type":"reasoning.text","text":"bob@example.com","signature":"SIG"}`,
		`{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"email\":\"bob@example.com\"}"},"extra_content":{"google":{"thought_signature":"SIG"}}}`,
	}
	for mode, compiled := range signedTestRules(t) {
		for _, block := range signed {
			body := `{"messages":[{"role":"user","content":"alice@example.com"},{"role":"assistant","content":[` + block + `]}]}`
			got, err := compiled.ApplyWithCipher([]byte(body), cipher)
			if err != nil {
				t.Fatalf("%s %s: %v", mode, block, err)
			}
			if out := gjson.GetBytes(got, "messages.1.content.0").Raw; out != block {
				t.Errorf("%s: signed content changed:\n got: %s\nwant: %s", mode, out, block)
			}
			if mode != "none" && gjson.GetBytes(got, "messages.0.content").Str == "alice@example.com" {
				t.Errorf("%s: unsigned user content was not protected", mode)
			}
		}
	}
}

// 同一签名块里既有网关还原的值，又有模型自己写的相同文字：只换回记录的位置。
func TestApplyRestoresOnlyRecordedPositions(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream := `{"type":"thinking","thinking":"user ` + token + ` asked; model also wrote alice@example.com","signature":"SIG"}`
	visible := "user alice@example.com asked; model also wrote alice@example.com"
	wrapped := WrapSignature("SIG", []SignedString{{Path: []any{"thinking"}, Value: visible,
		Spans: []RestoreSpan{{Offset: 5, Length: len("alice@example.com"), Original: token}}}})
	client := `{"type":"thinking","thinking":` + signedTestString(t, visible) + `,"signature":` + signedTestString(t, wrapped) + `}`
	for mode, compiled := range signedTestRules(t) {
		body := `{"messages":[{"role":"assistant","content":[` + client + `]}]}`
		got, err := compiled.ApplyWithCipher([]byte(body), cipher)
		if err != nil || gjson.GetBytes(got, "messages.0.content.0").Raw != upstream {
			t.Errorf("%s: signed content = %s / %v, want %s", mode, gjson.GetBytes(got, "messages.0.content.0").Raw, err, upstream)
		}
	}
}

// 工具参数里的 name、id、data 等是业务数据，按记录路径换回，不能当成协议字段跳过。
func TestApplyRestoresRecordedToolArgumentsWithProtocolNames(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream := `{"functionCall":{"name":"send","args":{"name":"` + token + `","id":"` + token + `","data":"bob@example.com"}},"thoughtSignature":"SIG"}`
	span := []RestoreSpan{{Offset: 0, Length: len("alice@example.com"), Original: token}}
	wrapped := WrapSignature("SIG", []SignedString{
		{Path: []any{"functionCall", "args", "name"}, Value: "alice@example.com", Spans: span},
		{Path: []any{"functionCall", "args", "id"}, Value: "alice@example.com", Spans: span},
		{Path: []any{"functionCall", "args", "data"}, Value: "bob@example.com"},
	})
	client := `{"functionCall":{"name":"send","args":{"name":"alice@example.com","id":"alice@example.com","data":"bob@example.com"}},"thoughtSignature":` + signedTestString(t, wrapped) + `}`
	for mode, compiled := range signedTestRules(t) {
		body := `{"contents":[{"role":"model","parts":[` + client + `]}]}`
		got, err := compiled.ApplyWithCipher([]byte(body), cipher)
		if err != nil || gjson.GetBytes(got, "contents.0.parts.0").Raw != upstream {
			t.Errorf("%s: signed call = %s / %v, want %s", mode, gjson.GetBytes(got, "contents.0.parts.0").Raw, err, upstream)
		}
	}
}

// 回填只用记录里的原文：换用其他 AccessKey，或记录来自其他网关实例，都能还原。
func TestApplyRestoresRecordWithoutDecryption(t *testing.T) {
	token, err := signedTestCipher(t, 4).EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	upstream := `{"type":"thinking","thinking":"user ` + token + `","signature":"SIG"}`
	wrapped := WrapSignature("SIG", []SignedString{{Path: []any{"thinking"}, Value: "user alice@example.com",
		Spans: []RestoreSpan{{Offset: 5, Length: len("alice@example.com"), Original: token}}}})
	body := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"user alice@example.com","signature":` + signedTestString(t, wrapped) + `}]}]}`
	for mode, compiled := range signedTestRules(t) {
		got, err := compiled.ApplyWithCipher([]byte(body), signedTestCipher(t, 3))
		if err != nil || gjson.GetBytes(got, "messages.0.content.0").Raw != upstream {
			t.Errorf("%s: signed content = %s / %v, want %s", mode, gjson.GetBytes(got, "messages.0.content.0").Raw, err, upstream)
		}
	}
	// 串联的网关各拆一层：外层记录换回后，签名仍是上一级网关的包装。
	inner := WrapSignature("SIG", []SignedString{{Path: []any{"thinking"}, Value: "user " + token}})
	outer := WrapSignature(inner, []SignedString{{Path: []any{"thinking"}, Value: "user alice@example.com",
		Spans: []RestoreSpan{{Offset: 5, Length: len("alice@example.com"), Original: token}}}})
	chained := `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"user alice@example.com","signature":` + signedTestString(t, outer) + `}]}]}`
	got, err := signedTestRules(t)["none"].ApplyWithCipher([]byte(chained), signedTestCipher(t, 3))
	if err != nil || gjson.GetBytes(got, "messages.0.content.0.thinking").Str != "user "+token ||
		gjson.GetBytes(got, "messages.0.content.0.signature").Str != inner {
		t.Fatalf("chained record = %s / %v", got, err)
	}
}

// 客户端改动过签名内容（例如合并了 Gemini 分片）：按普通上游内容加密，换回原签名。
func TestApplyEncryptsChangedSignedContent(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	wrapped := WrapSignature("SIG", []SignedString{{Path: []any{"text"}, Value: "; test with bob@example.com"}})
	merged := `{"text":"user alice@example.com; test with bob@example.com","thoughtSignature":` + signedTestString(t, wrapped) + `}`
	damaged := `{"text":"user alice@example.com","thoughtSignature":"` + signatureWrapperPrefix + `!!"}`
	for mode, compiled := range signedTestRules(t) {
		for _, part := range []string{merged, damaged} {
			body := `{"contents":[{"role":"model","parts":[` + part + `]}]}`
			got, err := compiled.ApplyWithCipher([]byte(body), cipher)
			if err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			text := gjson.GetBytes(got, "contents.0.parts.0.text").Str
			if mode != "none" && strings.Contains(text, "alice@example.com") {
				t.Errorf("%s: changed signed content leaked plaintext: %s", mode, got)
			}
			signature := gjson.GetBytes(got, "contents.0.parts.0.thoughtSignature").Str
			if part == merged && signature != "SIG" {
				t.Errorf("%s: original signature was not restored: %s", mode, got)
			}
		}
	}
}

// 记录必须覆盖签名块里的全部字符串：客户端改动或新增记录以外的字段、签名之后才到的内容，
// 都按普通上游内容加密，不能随记录核对通过而原样外发。
func TestApplyEncryptsSignedContentOutsideRecord(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	span := []RestoreSpan{{Offset: 0, Length: len("alice@example.com"), Original: token}}
	onlyTo := WrapSignature("SIG", []SignedString{{Path: []any{"functionCall", "args", "to"}, Value: "alice@example.com", Spans: span}})
	full := WrapSignature("SIG", []SignedString{
		{Path: []any{"functionCall", "args", "to"}, Value: "alice@example.com", Spans: span},
		{Path: []any{"functionCall", "args", "cc"}, Value: "public"},
	})
	parts := map[string]string{
		"changed unrecorded field": `{"functionCall":{"name":"send","args":{"to":"alice@example.com","cc":"bob@example.com"}},"thoughtSignature":` + signedTestString(t, onlyTo) + `}`,
		"added field":              `{"functionCall":{"name":"send","args":{"to":"alice@example.com","cc":"public","bcc":"bob@example.com"}},"thoughtSignature":` + signedTestString(t, full) + `}`,
		"content after signature":  `{"functionCall":{"name":"send","args":{"to":"alice@example.com"}},"thoughtSignature":` + signedTestString(t, WrapSignature("SIG", nil)) + `}`,
	}
	for mode, compiled := range signedTestRules(t) {
		if mode == "none" {
			continue
		}
		for name, part := range parts {
			got, err := compiled.ApplyWithCipher([]byte(`{"contents":[{"role":"model","parts":[`+part+`]}]}`), cipher)
			if err != nil || strings.Contains(string(got), "@example.com") ||
				gjson.GetBytes(got, "contents.0.parts.0.thoughtSignature").Str != "SIG" {
				t.Errorf("%s/%s: signed content outside the record was not encrypted: %s / %v", mode, name, got, err)
			}
		}
	}
}

// 签名只认协议位置：用户消息、工具结果里带 signature 的对象仍按业务数据脱敏。
func TestApplyTreatsSignatureOutsideModelContentAsData(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	compiled := signedTestRules(t)["encrypt"]
	bodies := map[string]string{
		"user content": `{"messages":[{"role":"user","content":[{"type":"thinking","thinking":"alice@example.com","signature":"SIG"}]}]}`,
		"tool result":  `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"alice@example.com","signature":"SIG"}]}]}]}`,
		"tool input":   `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"sign","input":{"type":"thinking","email":"alice@example.com","signature":"SIG"}}]}]}`,
		"gemini args":  `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"sign","args":{"email":"alice@example.com","thoughtSignature":"SIG"}}}]}]}`,
	}
	for name, body := range bodies {
		got, err := compiled.ApplyWithCipher([]byte(body), cipher)
		if err != nil || strings.Contains(string(got), "alice@example.com") {
			t.Errorf("%s: business data was not redacted: %s / %v", name, got, err)
		}
	}
}

func TestApplyKeepsOpaqueProtocolFields(t *testing.T) {
	cipher := signedTestCipher(t, 3)
	compiled, err := Compile([]Rule{{Pattern: `anthropic|Eo8BCio[A-Za-z0-9]+`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"messages":[{"role":"assistant","content":[{"type":"text","text":"anthropic","citations":[{"type":"web_search_result_location","url":"https://example.com","encrypted_index":"Eo8BCioIAhgB"}]}],` +
		`"reasoning_details":[{"type":"reasoning.encrypted","data":"Eo8BCioIAhgB","format":"anthropic-claude-v1"}]}]}`
	got, err := compiled.ApplyWithCipher([]byte(body), cipher)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "messages.0.content.0.citations.0.encrypted_index").Str != "Eo8BCioIAhgB" ||
		gjson.GetBytes(got, "messages.0.reasoning_details.0.format").Str != "anthropic-claude-v1" ||
		gjson.GetBytes(got, "messages.0.content.0.text").Str == "anthropic" {
		t.Fatalf("opaque protocol fields were rewritten or text was not: %s", got)
	}
}
