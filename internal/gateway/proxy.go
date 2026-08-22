package gateway

import (
	"crypto/subtle"
	"fmt"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
)

func resolveAttemptProxy(
	encryptionService encryption.Service,
	groupProxy outboundproxy.Effective,
	ref state.CredentialRef,
) (outboundproxy.Effective, string, error) {
	if encryptionService == nil {
		return outboundproxy.Effective{}, "", fmt.Errorf("proxy encryption service is unavailable")
	}
	groupProxy, err := outboundproxy.NormalizeEffective(groupProxy)
	if err != nil {
		return outboundproxy.Effective{}, "", fmt.Errorf("group proxy config is invalid")
	}
	if ref.EncryptedProxy == "" {
		if ref.ProxyFingerprint != "" {
			return outboundproxy.Effective{}, "", fmt.Errorf("credential proxy identity is invalid")
		}
		fingerprint, err := effectiveProxyFingerprint(encryptionService, groupProxy)
		return groupProxy, fingerprint, err
	}
	if ref.ProxyFingerprint == "" {
		return outboundproxy.Effective{}, "", fmt.Errorf("credential proxy identity is invalid")
	}
	plaintext, err := encryptionService.Decrypt(ref.EncryptedProxy)
	if err != nil {
		return outboundproxy.Effective{}, "", fmt.Errorf("decrypt credential proxy config")
	}
	fingerprint := encryptionService.Hash(plaintext)
	if subtle.ConstantTimeCompare([]byte(fingerprint), []byte(ref.ProxyFingerprint)) != 1 {
		plaintext = ""
		return outboundproxy.Effective{}, "", fmt.Errorf("credential proxy fingerprint mismatch")
	}
	config, err := outboundproxy.Decode(plaintext)
	plaintext = ""
	if err != nil || config.Mode == outboundproxy.ModeInherit {
		return outboundproxy.Effective{}, "", fmt.Errorf("credential proxy config is invalid")
	}
	effective, err := outboundproxy.Resolve(&config, nil, nil, nil)
	if err != nil {
		return outboundproxy.Effective{}, "", fmt.Errorf("credential proxy config is invalid")
	}
	return effective, fingerprint, nil
}

func effectiveProxyFingerprint(
	encryptionService encryption.Service,
	effective outboundproxy.Effective,
) (string, error) {
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return "", fmt.Errorf("proxy config is invalid")
	}
	if effective.Config.Mode == outboundproxy.ModeEnvironment {
		return encryptionService.Hash(`{"mode":"environment"}`), nil
	}
	encoded, err := outboundproxy.Encode(effective.Config)
	if err != nil {
		return "", fmt.Errorf("proxy config is invalid")
	}
	return encryptionService.Hash(encoded), nil
}
