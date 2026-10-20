package gateway

import (
	"time"

	"gpt-load/internal/affinity"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

// encryptedLineageKey derives the same tenant- and protocol-scoped session
// identity used for affinity. Explicit client signals win; the prompt prefix
// is the stable fallback when Codex sends full growing history.
func (handler *Handler) encryptedLineageKey(
	accessKeyID uint,
	clientProtocol protocol.Protocol,
	metadata dialect.RequestMetadata,
) string {
	if handler == nil || handler.encryption == nil || accessKeyID == 0 || !clientProtocol.Valid() {
		return ""
	}
	var key affinity.Key
	switch {
	case metadata.PromptCacheKey != "":
		key = affinity.DerivePromptCacheKey(handler.encryption, accessKeyID, clientProtocol, metadata.PromptCacheKey)
	case metadata.PreviousResponseID != "":
		key = affinity.DeriveKey(
			handler.encryption,
			accessKeyID,
			clientProtocol,
			[]byte("codex-encrypted-lineage\x00previous-response\x00"+metadata.PreviousResponseID),
		)
	case len(metadata.AffinityPrefix) > 0:
		key = affinity.DeriveKey(handler.encryption, accessKeyID, clientProtocol, metadata.AffinityPrefix)
	}
	if !key.Valid() {
		return ""
	}
	return string(key)
}

func (handler *Handler) encryptedLineageNow() time.Time {
	if handler != nil && handler.now != nil {
		return handler.now()
	}
	return time.Now()
}
