package gateway

import (
	"gpt-load/internal/affinity"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

type requestAffinity struct {
	key                   affinity.Key
	observation           affinity.Observation
	preferredCredentialID uint
}

func (handler *Handler) resolveRequestAffinity(
	accessKeyID uint,
	clientProtocol protocol.Protocol,
	externalModel string,
	prefix []byte,
	allowedCredentialRefs map[uint]state.CredentialRef,
) requestAffinity {
	if handler == nil || handler.affinityCache == nil {
		return requestAffinity{}
	}
	key := affinity.DeriveKey(
		handler.encryption,
		accessKeyID,
		clientProtocol,
		externalModel,
		prefix,
	)
	if !key.Valid() {
		return requestAffinity{}
	}
	observation := handler.affinityCache.Lookup(key)
	resolved := requestAffinity{key: key, observation: observation}
	if !observation.Found() {
		return resolved
	}
	target := observation.Target
	ref, allowed := allowedCredentialRefs[target.CredentialID]
	if !allowed || ref.GroupID != target.GroupID ||
		ref.IdentityGeneration != target.IdentityGeneration {
		return resolved
	}
	resolved.preferredCredentialID = target.CredentialID
	return resolved
}

func (handler *Handler) recordAffinitySuccess(
	request requestAffinity,
	selection scheduler.Selection,
	ref state.CredentialRef,
) {
	if handler == nil || handler.affinityCache == nil || !request.key.Valid() {
		return
	}
	handler.affinityCache.RecordSuccess(
		request.key,
		request.observation,
		affinity.Target{
			GroupID: selection.GroupID, CredentialID: selection.CredentialID,
			IdentityGeneration: ref.IdentityGeneration,
		},
	)
}
