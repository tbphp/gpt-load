// Package importfile translates supported subscription exports into channel credentials.
package importfile

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gpt-load/internal/channel"
)

const (
	MaxFileBytes       = 4 << 20
	MaxCredentialBytes = 64 << 10
	MaxEntries         = 1000
)

type Document struct {
	Entries []Entry
}

type Entry struct {
	Index      int
	Format     string
	ChannelID  channel.ID
	Credential []byte
	ErrorCode  string
}

type Error struct {
	Code string
}

func (err *Error) Error() string { return err.Code }

// Parse only identifies and translates files; channel drivers verify account identities.
func Parse(raw []byte) (Document, error) {
	if len(raw) > MaxFileBytes {
		return Document{}, &Error{Code: "file_too_large"}
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(raw) {
		return Document{}, &Error{Code: "invalid_json"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return Document{}, &Error{Code: "invalid_json"}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Document{}, &Error{Code: "invalid_json"}
	}
	var values []any
	format := ""
	switch root := value.(type) {
	case []any:
		values = root
	case map[string]any:
		kind, _ := root["type"].(string)
		_, hasAccounts := root["accounts"]
		if kind == "sub2api-data" || kind == "sub2api-bundle" || hasAccounts {
			if _, exists := root["type"]; exists {
				if _, ok := root["type"].(string); !ok {
					return Document{}, &Error{Code: "unsupported_format"}
				}
			}
			if kind != "" && kind != "sub2api-data" && kind != "sub2api-bundle" {
				return Document{}, &Error{Code: "unsupported_format"}
			}
			if kind == "" {
				exportedAt, ok := root["exported_at"].(string)
				if !ok || strings.TrimSpace(exportedAt) == "" {
					return Document{}, &Error{Code: "unsupported_format"}
				}
			}
			if version, exists := root["version"]; exists && version != json.Number("0") && version != json.Number("1") {
				return Document{}, &Error{Code: "unsupported_version"}
			}
			accounts, ok := root["accounts"].([]any)
			_, proxiesOK := root["proxies"].([]any)
			if !ok || !proxiesOK {
				return Document{}, &Error{Code: "invalid_document"}
			}
			values, format = accounts, "sub2api"
		} else {
			values = []any{root}
		}
	default:
		return Document{}, &Error{Code: "invalid_document"}
	}
	if len(values) == 0 {
		return Document{}, &Error{Code: "empty_document"}
	}
	if len(values) > MaxEntries {
		return Document{}, &Error{Code: "too_many_entries"}
	}
	doc := Document{Entries: make([]Entry, 0, len(values))}
	for index, value := range values {
		entry := parseEntry(value, format)
		entry.Index = index + 1
		doc.Entries = append(doc.Entries, entry)
	}
	return doc, nil
}

// decodeValue rejects duplicate fields without imposing canonical JSON's numeric rules
// on third-party metadata such as fractional rates or prices.
func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, &Error{Code: "invalid_json"}
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		value := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, &Error{Code: "invalid_json"}
			}
			if _, exists := value[key]; exists {
				return nil, &Error{Code: "invalid_json"}
			}
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value[key] = child
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return nil, &Error{Code: "invalid_json"}
		}
		return value, nil
	case json.Delim('['):
		value := make([]any, 0)
		for decoder.More() {
			child, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			value = append(value, child)
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return nil, &Error{Code: "invalid_json"}
		}
		return value, nil
	default:
		return token, nil
	}
}

func parseEntry(value any, format string) Entry {
	entry := Entry{Format: format}
	object, ok := value.(map[string]any)
	if !ok {
		entry.ErrorCode = "invalid_credential"
		return entry
	}
	_, hasCodexTokens := object["tokens"]
	_, hasClaudeTokens := object["claudeAiOauth"]
	_, hasOpenAIKey := object["OPENAI_API_KEY"]
	if hasClaudeTokens && (hasCodexTokens || hasOpenAIKey) {
		entry.ErrorCode = "unsupported_format"
		return entry
	}
	var mapped map[string]any
	if format == "sub2api" || object["platform"] != nil && object["credentials"] != nil {
		entry.Format = "sub2api"
		entry.ChannelID = subscriptionChannel(stringValue(object["platform"]))
		mapped, entry.ErrorCode = mapSub2API(object, entry.ChannelID)
	} else if channelID := cpaChannel(stringValue(object["type"])); channelID != "" {
		entry.Format, entry.ChannelID = "cpa", channelID
		// 保留全部字段，不能绕开现有 CPA 解析器的字段与控制元数据校验。
		mapped = object
		if _, ok := object["refresh_token"].(string); !ok || strings.TrimSpace(stringValue(object["refresh_token"])) == "" {
			entry.ErrorCode = "missing_refresh_token"
		}
	} else if _, exists := object["claudeAiOauth"]; exists {
		entry.Format, entry.ChannelID = "claude-code", channel.Claude
		mapped, entry.ErrorCode = mapClaudeCode(object)
	} else if hasCodexTokens || hasOpenAIKey {
		entry.Format, entry.ChannelID = "codex", channel.Codex
		mapped, entry.ErrorCode = mapCodex(object)
	} else {
		entry.ErrorCode = "unsupported_format"
	}
	if entry.ErrorCode != "" {
		return entry
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		entry.ErrorCode = "invalid_credential"
	} else if len(encoded) > MaxCredentialBytes {
		clear(encoded)
		entry.ErrorCode = "credential_too_large"
	} else {
		entry.Credential = encoded
	}
	return entry
}

func mapCodex(object map[string]any) (map[string]any, string) {
	mode, modeOK := optionalString(object, "auth_mode")
	apiKey, keyOK := optionalString(object, "OPENAI_API_KEY")
	if !modeOK || !keyOK {
		return nil, "invalid_credential"
	}
	if mode != "" && mode != "chatgpt" || mode == "" && apiKey != "" {
		return nil, "unsupported_auth_mode"
	}
	tokens, ok := object["tokens"].(map[string]any)
	if !ok {
		return nil, "invalid_credential"
	}
	if tooLarge(tokens) {
		return nil, "credential_too_large"
	}
	m := newMapper("codex")
	for _, key := range []string{"access_token", "refresh_token", "id_token", "account_id"} {
		m.copyString(key, tokens, key)
	}
	m.copyString("last_refresh", object, "last_refresh")
	m.validateClient(tokens, channel.Codex)
	m.validateClient(object, channel.Codex)
	m.requireRefresh()
	return m.result()
}

func mapClaudeCode(object map[string]any) (map[string]any, string) {
	tokens, ok := object["claudeAiOauth"].(map[string]any)
	if !ok {
		return nil, "invalid_credential"
	}
	if tooLarge(tokens) {
		return nil, "credential_too_large"
	}
	m := newMapper("claude")
	m.copyString("access_token", tokens, "accessToken")
	m.copyString("refresh_token", tokens, "refreshToken")
	m.copyExpiry(tokens, "expiresAt", true)
	if scopes, exists := tokens["scopes"]; exists {
		items, ok := scopes.([]any)
		if !ok {
			m.fail("invalid_credential")
		} else {
			inference := false
			for _, item := range items {
				scope, ok := item.(string)
				if !ok {
					m.fail("invalid_credential")
				}
				inference = inference || scope == "user:inference"
			}
			if !inference {
				m.fail("unsupported_auth_mode")
			}
		}
	}
	m.validateClient(tokens, channel.Claude)
	m.requireRefresh()
	return m.result()
}

func mapSub2API(object map[string]any, channelID channel.ID) (map[string]any, string) {
	if channelID == "" {
		return nil, "unsupported_channel"
	}
	if stringValue(object["type"]) != "oauth" {
		return nil, "unsupported_auth_mode"
	}
	credentials, ok := object["credentials"].(map[string]any)
	if !ok {
		return nil, "invalid_credential"
	}
	if tooLarge(credentials) {
		return nil, "credential_too_large"
	}
	extra := map[string]any{}
	if value, exists := object["extra"]; exists && value != nil {
		var ok bool
		extra, ok = value.(map[string]any)
		if !ok {
			return nil, "invalid_credential"
		}
	}
	m := newMapper(string(channelID))
	for _, key := range []string{"auth_mode", "openai_auth_mode"} {
		value, ok := optionalString(credentials, key)
		if !ok {
			m.fail("invalid_credential")
		} else if value != "" && value != "oauth" && value != "chatgpt" {
			m.fail("unsupported_auth_mode")
		}
	}
	m.validateClient(credentials, channelID)
	for _, key := range []string{"access_token", "refresh_token", "id_token", "last_refresh"} {
		if channelID == channel.Antigravity && key == "id_token" {
			continue
		}
		m.copyString(key, credentials, key)
	}
	m.copyExpiry(credentials, "expires_at", false)
	m.copyIdentity("email", []map[string]any{credentials, extra}, "email", "email_address")
	switch channelID {
	case channel.Codex:
		m.copyIdentity("account_id", []map[string]any{credentials, extra}, "chatgpt_account_id", "account_id")
	case channel.Claude:
		m.copyIdentity("account_uuid", []map[string]any{credentials, extra}, "account_uuid")
		m.copyIdentity("organization_uuid", []map[string]any{credentials, extra}, "org_uuid", "organization_uuid")
	case channel.Antigravity:
		m.copyIdentity("account_id", []map[string]any{credentials, extra}, "account_id")
		m.copyIdentity("project_id", []map[string]any{credentials}, "project_id")
		if stringValue(m.out["access_token"]) == "" || stringValue(m.out["email"]) == "" || stringValue(m.out["expired"]) == "" {
			m.fail("incomplete_credential")
		}
	case channel.Grok:
		m.copyIdentity("account_id", []map[string]any{credentials, extra}, "sub", "account_id")
		m.copyString("token_type", credentials, "token_type")
		if stringValue(m.out["access_token"]) == "" {
			m.fail("incomplete_credential")
		}
	}
	if tokenType, ok := optionalString(credentials, "token_type"); !ok {
		m.fail("invalid_credential")
	} else if tokenType != "" && !strings.EqualFold(tokenType, "Bearer") {
		m.fail("unsupported_auth_mode")
	}
	m.requireRefresh()
	return m.result()
}

type mapper struct {
	out  map[string]any
	code string
}

func newMapper(provider string) *mapper {
	return &mapper{out: map[string]any{"type": provider}}
}

func (m *mapper) fail(code string) {
	if m.code == "" {
		m.code = code
	}
}

func (m *mapper) result() (map[string]any, string) {
	if m.code != "" {
		return nil, m.code
	}
	return m.out, ""
}

func (m *mapper) copyString(destination string, source map[string]any, key string) {
	value, ok := optionalString(source, key)
	if !ok {
		m.fail("invalid_credential")
	} else if value != "" {
		m.out[destination] = value
	}
}

func (m *mapper) copyIdentity(destination string, sources []map[string]any, keys ...string) {
	var identity string
	for _, source := range sources {
		for _, key := range keys {
			value, ok := optionalString(source, key)
			if !ok {
				m.fail("invalid_credential")
				continue
			}
			if value == "" {
				continue
			}
			equal := identity == value
			if destination == "email" {
				equal = strings.EqualFold(identity, value)
			}
			if identity != "" && !equal {
				m.fail("identity_conflict")
			}
			identity = value
		}
	}
	if identity != "" {
		m.out[destination] = identity
	}
}

func (m *mapper) copyExpiry(source map[string]any, key string, milliseconds bool) {
	value, exists := source[key]
	if !exists || value == nil || value == "" {
		return
	}
	var timestamp time.Time
	var number string
	switch value := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && !milliseconds {
			timestamp = parsed
		} else {
			number = value
		}
	case json.Number:
		number = string(value)
	default:
		m.fail("invalid_credential")
		return
	}
	if number != "" {
		value, err := strconv.ParseInt(number, 10, 64)
		if err != nil || value <= 0 {
			m.fail("invalid_credential")
			return
		}
		if milliseconds {
			timestamp = time.UnixMilli(value)
		} else {
			timestamp = time.Unix(value, 0)
		}
	}
	if timestamp.IsZero() || timestamp.Year() < 1970 || timestamp.Year() > 9999 {
		m.fail("invalid_credential")
		return
	}
	m.out["expired"] = timestamp.UTC().Format(time.RFC3339Nano)
}

func (m *mapper) requireRefresh() {
	if stringValue(m.out["refresh_token"]) == "" {
		m.fail("missing_refresh_token")
	}
}

func (m *mapper) validateClient(source map[string]any, channelID channel.ID) {
	client, ok := optionalString(source, "client_id")
	if !ok {
		m.fail("invalid_credential")
		return
	}
	var expected string
	switch channelID {
	case channel.Codex:
		expected = "app_EMoamEEZ73f0CkXaXp7hrann"
	case channel.Claude:
		expected = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	case channel.Antigravity:
		expected = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	case channel.Grok:
		expected = "b1a00492-073a-47ea-816f-4c329264a828"
	}
	if client != "" && client != expected {
		m.fail("unsupported_client")
	}
}

func optionalString(source map[string]any, key string) (string, bool) {
	value, exists := source[key]
	if !exists || value == nil {
		return "", true
	}
	text, ok := value.(string)
	if !ok || strings.ContainsAny(text, "\r\n\x00") {
		return "", false
	}
	return strings.TrimSpace(text), true
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func tooLarge(value any) bool {
	encoded, err := json.Marshal(value)
	defer clear(encoded)
	return err != nil || len(encoded) > MaxCredentialBytes
}

func cpaChannel(provider string) channel.ID {
	switch strings.ToLower(provider) {
	case "codex":
		return channel.Codex
	case "claude":
		return channel.Claude
	case "antigravity":
		return channel.Antigravity
	case "grok", "xai":
		return channel.Grok
	default:
		return ""
	}
}

func subscriptionChannel(platform string) channel.ID {
	switch platform {
	case "openai":
		return channel.Codex
	case "anthropic":
		return channel.Claude
	case "antigravity":
		return channel.Antigravity
	case "grok":
		return channel.Grok
	default:
		return ""
	}
}
