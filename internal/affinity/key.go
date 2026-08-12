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
	if hasher == nil || accessKeyID == 0 || !clientProtocol.Valid() || len(prefix) == 0 {
		return ""
	}
	var material bytes.Buffer
	writeKeyField(&material, []byte(keyDomain))
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
