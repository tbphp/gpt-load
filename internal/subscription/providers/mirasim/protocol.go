package mirasim

import (
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

const (
	signatureVersion               = "mrs-sig-v2"
	sealVersion                    = "mrs-seal-v1"
	defaultSealPublicKeyBase64     = "HlyNMMeGXryasYLJuYQ/9ksCD4AYVVy1zXKAtJdpJn4="
	headerMirasimDevice            = "x-mirasim-device"
	headerMirasimTimestamp         = "x-mirasim-ts"
	headerMirasimNonce             = "x-mirasim-nonce"
	headerMirasimSignature         = "x-mirasim-sig"
	headerMirasimClient            = "x-mirasim-client"
	headerMirasimEncryptedMetadata = "x-mirasim-enc"
	headerMirasimSession           = "x-mirasim-session"
	headerMirasimAgent             = "x-mirasim-agent"
	headerMirasimCall              = "x-mirasim-call"
)

var signatureHeaderNames = map[string]struct{}{
	headerMirasimDevice:    {},
	headerMirasimTimestamp: {},
	headerMirasimNonce:     {},
	headerMirasimSignature: {},
}

type signingInput struct {
	Method        string
	Path          string
	Timestamp     string
	Nonce         string
	DeviceID      string
	ClientVersion string
	Credential    string
	Metadata      map[string]string
	Body          []byte
}

// canonicalSignaturePayload mirrors the 0.0.260 crypto core. In particular,
// an empty metadata set contributes an empty line rather than SHA-256("").
func canonicalSignaturePayload(input signingInput) ([]byte, error) {
	fields := []string{
		strings.ToUpper(strings.TrimSpace(input.Method)),
		input.Path,
		input.Timestamp,
		input.Nonce,
		input.DeviceID,
		input.ClientVersion,
		input.Credential,
	}
	for _, field := range fields {
		if strings.IndexByte(field, 0) >= 0 {
			return nil, fmt.Errorf("Mirasim signature field contains NUL")
		}
	}

	metadataCanonical, errMetadata := canonicalMetadata(input.Metadata)
	if errMetadata != nil {
		return nil, errMetadata
	}
	metadataDigest := ""
	if metadataCanonical != "" {
		metadataDigest = sha256Hex([]byte(metadataCanonical))
	}

	payload := strings.Join([]string{
		signatureVersion,
		fields[0],
		fields[1],
		fields[2],
		fields[3],
		fields[4],
		fields[5],
		sha256Hex([]byte(fields[6])),
		metadataDigest,
		sha256Hex(input.Body),
	}, "\n")
	return []byte(payload), nil
}

func canonicalMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "", nil
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.ToLower(key)
		if value == "" {
			continue
		}
		if strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("Mirasim metadata contains NUL")
		}
		normalized[key] = value
	}
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+":"+normalized[key])
	}
	return strings.Join(pairs, "\n"), nil
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func (c *Client) signatureHeadersLocked(method, requestPath, credential string, metadata map[string]string, body []byte) (http.Header, error) {
	if errSigner := c.loadSignerLocked(); errSigner != nil {
		return nil, errSigner
	}
	nonceBytes := make([]byte, 12)
	if _, errRandom := io.ReadFull(rand.Reader, nonceBytes); errRandom != nil {
		return nil, fmt.Errorf("generate Mirasim signature nonce: %w", errRandom)
	}
	input := signingInput{
		Method:        method,
		Path:          requestPath,
		Timestamp:     strconv.FormatInt(time.Now().UnixMilli(), 10),
		Nonce:         base64.RawURLEncoding.EncodeToString(nonceBytes),
		DeviceID:      c.deviceID,
		ClientVersion: c.storage.ClientVersion,
		Credential:    credential,
		Metadata:      metadata,
		Body:          body,
	}
	payload, errCanonical := canonicalSignaturePayload(input)
	if errCanonical != nil {
		return nil, errCanonical
	}
	signature := ed25519.Sign(c.privateKey, payload)
	headers := make(http.Header, len(metadata)+5)
	for key, value := range metadata {
		if value != "" {
			headers.Set(key, value)
		}
	}
	headers.Set(headerMirasimDevice, input.DeviceID)
	headers.Set(headerMirasimTimestamp, input.Timestamp)
	headers.Set(headerMirasimNonce, input.Nonce)
	headers.Set(headerMirasimSignature, base64.RawURLEncoding.EncodeToString(signature))
	if input.ClientVersion != "" {
		headers.Set(headerMirasimClient, input.ClientVersion)
	}
	return headers, nil
}

func (c *Client) relayMetadataLocked(ctx context.Context, requestPath string) (map[string]string, error) {
	if c.sessionID == "" {
		sessionID, errSession := randomUUID(rand.Reader)
		if errSession != nil {
			return nil, fmt.Errorf("generate Mirasim session ID: %w", errSession)
		}
		c.sessionID = "mirasim_" + sessionID
	}
	// Every relay call the official client makes carries its own identifier, so
	// the service can correlate one attempt rather than a whole session. A
	// retry is a new call and gets a new one.
	callID, errCall := randomUUID(rand.Reader)
	if errCall != nil {
		return nil, fmt.Errorf("generate Mirasim call ID: %w", errCall)
	}
	metadata := map[string]string{
		headerMirasimSession: c.sessionID,
		headerMirasimAgent:   relayAgent(requestPath),
		headerMirasimCall:    callID,
	}
	if identity, ok := ctx.Value(requestIdentityKey{}).(requestIdentity); ok {
		if identity.session != "" {
			metadata[headerMirasimSession] = "mirasim_" + sha256Hex([]byte(c.storage.AccountID + "\x00" + identity.session))[:32]
		}
		if identity.turn != "" {
			metadata["x-mirasim-turn"] = identity.turn
		}
	}
	// Only a sub-account the token itself names belongs in this header. The
	// official client leaves it out when the signed-in identity has none, and
	// the user ID it also holds is a different field that is not sent.
	if value := safeMetadata(c.relayAccountID); value != "" {
		metadata["x-mirasim-account"] = value
	}
	if value := safeMetadata(c.options.Locale); value != "" {
		metadata["x-mirasim-locale"] = value
	}
	if c.options.Collect != nil && !*c.options.Collect {
		metadata["x-mirasim-collect"] = "off"
	}
	return metadata, nil
}

func relayAgent(requestPath string) string {
	if strings.HasPrefix(requestPath, "/v1/responses") || strings.HasPrefix(requestPath, "/v1/alpha/search") {
		return "codex"
	}
	return "claude"
}

func randomUUID(source io.Reader) (string, error) {
	raw := make([]byte, 16)
	if _, errRead := io.ReadFull(source, raw); errRead != nil {
		return "", errRead
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func sealRelayHeaders(headers http.Header, method, requestPath string) error {
	recipientPublic, errKey := relaySealPublicKey()
	if errKey != nil {
		return errKey
	}
	metadata := make(map[string]string)
	sealedNames := make([]string, 0)
	for name := range headers {
		lowerName := strings.ToLower(name)
		if !isSealedRelayHeader(lowerName) {
			continue
		}
		value := headers.Get(name)
		if value == "" {
			continue
		}
		metadata[lowerName] = value
		sealedNames = append(sealedNames, name)
	}
	if len(metadata) == 0 {
		return nil
	}
	plaintext, errJSON := json.Marshal(metadata)
	if errJSON != nil {
		return fmt.Errorf("encode Mirasim relay metadata: %w", errJSON)
	}
	ephemeralSecret := make([]byte, curve25519.ScalarSize)
	if _, errRandom := io.ReadFull(rand.Reader, ephemeralSecret); errRandom != nil {
		return fmt.Errorf("generate Mirasim seal key: %w", errRandom)
	}
	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, errRandom := io.ReadFull(rand.Reader, nonce); errRandom != nil {
		return fmt.Errorf("generate Mirasim seal nonce: %w", errRandom)
	}
	aad := []byte(strings.Join([]string{sealVersion, strings.ToUpper(strings.TrimSpace(method)), requestPath}, "\n"))
	sealed, errSeal := sealPayload(recipientPublic, ephemeralSecret, nonce, plaintext, aad)
	if errSeal != nil {
		return errSeal
	}
	for _, name := range sealedNames {
		headers.Del(name)
	}
	headers.Set(headerMirasimEncryptedMetadata, base64.RawURLEncoding.EncodeToString(sealed))
	return nil
}

func isSealedRelayHeader(lowerName string) bool {
	return strings.HasPrefix(lowerName, "x-mirasim-") &&
		lowerName != headerMirasimClient &&
		lowerName != headerMirasimEncryptedMetadata
}

func relaySealPublicKey() ([]byte, error) {
	encoded := strings.TrimSpace(os.Getenv("MIRASIM_SEAL_PUBKEY"))
	if encoded == "" {
		encoded = defaultSealPublicKeyBase64
	}
	var publicKey []byte
	var errDecode error
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		publicKey, errDecode = encoding.DecodeString(encoded)
		if errDecode == nil {
			break
		}
	}
	if errDecode != nil {
		return nil, fmt.Errorf("MIRASIM_SEAL_PUBKEY is not valid base64")
	}
	if len(publicKey) != curve25519.PointSize {
		return nil, fmt.Errorf("MIRASIM_SEAL_PUBKEY must decode to 32 bytes, got %d", len(publicKey))
	}
	return publicKey, nil
}

func sealPayload(recipientPublic, ephemeralSecret, nonce, plaintext, aad []byte) ([]byte, error) {
	if len(recipientPublic) != curve25519.PointSize {
		return nil, fmt.Errorf("Mirasim relay seal public key must be 32 bytes")
	}
	if len(ephemeralSecret) != curve25519.ScalarSize {
		return nil, fmt.Errorf("Mirasim ephemeral seal key must be 32 bytes")
	}
	if len(nonce) != chacha20poly1305.NonceSize {
		return nil, fmt.Errorf("Mirasim seal nonce must be 12 bytes")
	}
	ephemeralPublic, errPublic := curve25519.X25519(ephemeralSecret, curve25519.Basepoint)
	if errPublic != nil {
		return nil, fmt.Errorf("derive Mirasim ephemeral public key: %w", errPublic)
	}
	sharedSecret, errShared := curve25519.X25519(ephemeralSecret, recipientPublic)
	if errShared != nil {
		return nil, fmt.Errorf("derive Mirasim relay shared key: %w", errShared)
	}
	key, errHKDF := hkdf.Key(sha256.New, sharedSecret, ephemeralPublic, sealVersion, chacha20poly1305.KeySize)
	if errHKDF != nil {
		return nil, fmt.Errorf("derive Mirasim relay seal key: %w", errHKDF)
	}
	aead, errAEAD := chacha20poly1305.New(key)
	if errAEAD != nil {
		return nil, fmt.Errorf("initialize Mirasim relay seal: %w", errAEAD)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	packed := make([]byte, 0, len(ephemeralPublic)+len(nonce)+len(ciphertext))
	packed = append(packed, ephemeralPublic...)
	packed = append(packed, nonce...)
	packed = append(packed, ciphertext...)
	return packed, nil
}
