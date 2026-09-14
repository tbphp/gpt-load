package affinity

import (
	"bytes"
	"encoding/binary"

	"gpt-load/internal/protocol"
)

const keyDomain = "gpt-load/affinity/prompt-prefix-hmac/v2"

// Key is an opaque, tenant-scoped affinity cache key.
type Key string

func (key Key) Valid() bool {
	return key != ""
}

// Hasher computes a keyed digest without exposing its key material.
type Hasher interface {
	Hash(string) string
}

// DeriveKey creates a versioned affinity key for one stable prompt prefix.
func DeriveKey(
	hasher Hasher,
	accessKeyID uint,
	clientProtocol protocol.Protocol,
	prefix []byte,
) Key {
	return deriveKey(hasher, accessKeyID, clientProtocol, keyDomain, prefix)
}

// DerivePromptCacheKey 与提示词使用不同命名空间，保持现有租户和协议隔离。
func DerivePromptCacheKey(hasher Hasher, accessKeyID uint, clientProtocol protocol.Protocol, key string) Key {
	return deriveKey(hasher, accessKeyID, clientProtocol, "gpt-load/affinity/prompt-cache-key/v1", []byte(key))
}

func deriveKey(hasher Hasher, accessKeyID uint, clientProtocol protocol.Protocol, domain string, prefix []byte) Key {
	if hasher == nil || accessKeyID == 0 || !clientProtocol.Valid() || len(prefix) == 0 {
		return ""
	}
	var material bytes.Buffer
	writeKeyField(&material, []byte(domain))
	var encodedID [8]byte
	binary.BigEndian.PutUint64(encodedID[:], uint64(accessKeyID))
	material.Write(encodedID[:])
	writeKeyField(&material, []byte(clientProtocol))
	writeKeyField(&material, prefix)
	return Key(hasher.Hash(material.String()))
}

func writeKeyField(target *bytes.Buffer, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	target.Write(size[:])
	target.Write(value)
}
