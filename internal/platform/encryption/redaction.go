package encryption

import (
	"crypto/aes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/tink-crypto/tink-go/v2/daead/subtle"
)

const (
	redactionTokenPrefix = "gld1_"
	redactionKeyDomain   = "gpt-load/request-redaction/aes-siv/v1/"
	// One token must fit comfortably inside the ordinary 32 MiB response limit.
	maxRedactionPlaintextBytes  = 16 << 20
	maxRedactionTokensPerText   = 65536
	redactionCiphertextOverhead = aes.BlockSize
)

var errInvalidRedactionToken = errors.New("invalid redaction token")

// RedactionCipher protects request text under one authenticated AccessKey.
// Its key is derived at construction and remains unchanged for its lifetime.
type RedactionCipher interface {
	EncryptToken(plaintext string) (string, error)
	TokenCandidateEnd(text string, start int) (end int, complete bool)
	ValidTokenAt(text string, start int) (end int, valid bool)
	// RestoreText 只还原通过认证的密文；无法认证的候选按原文保留。
	RestoreText(text string) (string, error)
	// UnrestoredTokens 返回按原文保留的候选次数，仅用于诊断日志。
	UnrestoredTokens() int64
}

type redactionCipher struct {
	siv        *subtle.AESSIV
	unrestored atomic.Int64
}

func (s *aesService) NewRedactionCipher(accessKeyID uint) (RedactionCipher, error) {
	if accessKeyID == 0 {
		return nil, fmt.Errorf("AccessKey ID is required for redaction")
	}
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(accessKeyID))
	key, err := hkdf.Key(sha256.New, s.rootKey, nil, redactionKeyDomain+string(id[:]), subtle.AESSIVKeySize)
	if err != nil {
		return nil, fmt.Errorf("derive redaction key: %w", err)
	}
	siv, err := subtle.NewAESSIV(key)
	if err != nil {
		return nil, fmt.Errorf("create redaction cipher: %w", err)
	}
	return &redactionCipher{siv: siv}, nil
}

func (c *redactionCipher) EncryptToken(plaintext string) (string, error) {
	if len(plaintext) > maxRedactionPlaintextBytes {
		return "", fmt.Errorf("redaction plaintext exceeds maximum size")
	}
	encodedLen := base64.RawURLEncoding.EncodedLen(len(plaintext) + redactionCiphertextOverhead)
	header := redactionTokenPrefix + strconv.Itoa(encodedLen) + "_"
	ciphertext, err := c.siv.EncryptDeterministically([]byte(plaintext), []byte(header))
	if err != nil {
		return "", fmt.Errorf("encrypt redaction token: %w", err)
	}
	if len(ciphertext) != len(plaintext)+redactionCiphertextOverhead {
		return "", fmt.Errorf("unexpected redaction ciphertext length")
	}
	return header + base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

type tokenCandidate struct {
	headerEnd int
	end       int
	found     bool
	malformed bool
	// badHeader 表示长度字段本身非法，流式解析在下划线处即放弃该候选。
	badHeader bool
}

func parseRedactionCandidate(text string, start int) tokenCandidate {
	if start < 0 || start > len(text)-len(redactionTokenPrefix) || !strings.HasPrefix(text[start:], redactionTokenPrefix) {
		return tokenCandidate{}
	}
	i := start + len(redactionTokenPrefix)
	if i >= len(text) || text[i] < '0' || text[i] > '9' {
		return tokenCandidate{}
	}
	maxEncodedLen := base64.RawURLEncoding.EncodedLen(maxRedactionPlaintextBytes + redactionCiphertextOverhead)
	length := 0
	oversize := false
	for ; i < len(text) && text[i] >= '0' && text[i] <= '9'; i++ {
		digit := int(text[i] - '0')
		if oversize || length > (maxEncodedLen-digit)/10 {
			oversize = true
			continue
		}
		length = length*10 + digit
	}
	if i >= len(text) || text[i] != '_' {
		return tokenCandidate{}
	}
	headerEnd := i + 1
	if oversize || length == 0 || (i > start+len(redactionTokenPrefix)+1 && text[start+len(redactionTokenPrefix)] == '0') {
		return tokenCandidate{headerEnd: headerEnd, found: true, malformed: true, badHeader: true}
	}
	candidate := tokenCandidate{headerEnd: headerEnd, end: headerEnd + length, found: true}
	if length < base64.RawURLEncoding.EncodedLen(redactionCiphertextOverhead) || length > len(text)-headerEnd {
		candidate.malformed = true
	}
	return candidate
}

// unrestoredCandidateEnd 与流式解析的放弃点一致：头部非法时止于下划线，
// 否则止于声明长度、文本结尾或首个非 Base64url 字符。
func unrestoredCandidateEnd(text string, candidate tokenCandidate) int {
	if candidate.badHeader {
		return candidate.headerEnd
	}
	end := min(candidate.end, len(text))
	for i := candidate.headerEnd; i < end; i++ {
		if !redactionBase64URLByte(text[i]) {
			return i
		}
	}
	return end
}

func redactionBase64URLByte(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
		(ch >= '0' && ch <= '9') || ch == '-' || ch == '_'
}

func (c *redactionCipher) decryptCandidate(text string, start int, candidate tokenCandidate) (string, error) {
	if !candidate.found || candidate.malformed {
		return "", errInvalidRedactionToken
	}
	encoded := text[candidate.headerEnd:candidate.end]
	for i := 0; i < len(encoded); i++ {
		if !redactionBase64URLByte(encoded[i]) {
			return "", errInvalidRedactionToken
		}
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) < redactionCiphertextOverhead {
		return "", errInvalidRedactionToken
	}
	plaintext, err := c.siv.DecryptDeterministically(data, []byte(text[start:candidate.headerEnd]))
	if err != nil {
		return "", errInvalidRedactionToken
	}
	return string(plaintext), nil
}

// TokenCandidateEnd 只解析有界候选，不分配解码缓冲，也不执行认证。
func (c *redactionCipher) TokenCandidateEnd(text string, start int) (int, bool) {
	candidate := parseRedactionCandidate(text, start)
	return candidate.end, candidate.found && !candidate.malformed
}

func (c *redactionCipher) ValidTokenAt(text string, start int) (end int, valid bool) {
	candidate := parseRedactionCandidate(text, start)
	if _, err := c.decryptCandidate(text, start, candidate); err != nil {
		return 0, false
	}
	return candidate.end, true
}

func (c *redactionCipher) RestoreText(text string) (string, error) {
	var out strings.Builder
	last := 0
	tokens := 0
	for scan := 0; scan < len(text); {
		relative := strings.Index(text[scan:], redactionTokenPrefix)
		if relative < 0 {
			break
		}
		start := scan + relative
		candidate := parseRedactionCandidate(text, start)
		if !candidate.found {
			scan = start + len(redactionTokenPrefix)
			continue
		}
		tokens++
		if tokens > maxRedactionTokensPerText {
			return "", fmt.Errorf("redaction token count exceeds maximum")
		}
		plaintext, err := c.decryptCandidate(text, start, candidate)
		if err != nil {
			// 模型抄错或截断的密文不含可泄露信息，按原文保留，不让整个响应失败。
			c.unrestored.Add(1)
			scan = unrestoredCandidateEnd(text, candidate)
			continue
		}
		if last == 0 && out.Len() == 0 {
			out.Grow(len(text))
		}
		out.WriteString(text[last:start])
		out.WriteString(plaintext)
		last = candidate.end
		scan = candidate.end
	}
	if last == 0 && out.Len() == 0 {
		return text, nil
	}
	out.WriteString(text[last:])
	return out.String(), nil
}

func (c *redactionCipher) UnrestoredTokens() int64 { return c.unrestored.Load() }
