package state

import (
	"fmt"
	"strings"

	"gpt-load/internal/concurrency"
	"gpt-load/internal/connection"
)

type CredentialConcurrency struct {
	Subject            concurrency.Subject
	GroupID            uint
	IdentityGeneration uint64
	Shared             bool
}

func compileConcurrency(input CompileInput, snapshot *ConfigSnapshot) error {
	snapshot.ConcurrencyPolicies = make(map[concurrency.Subject]int64, len(input.ConcurrencyPolicies))
	for subject, limit := range input.ConcurrencyPolicies {
		if subject == "" || len(subject) > 255 || limit < 0 || limit > concurrency.MaximumLimit {
			return fmt.Errorf("invalid concurrency policy")
		}
		snapshot.ConcurrencyPolicies[subject] = limit
	}
	snapshot.CredentialConcurrency = make(map[uint]CredentialConcurrency, len(input.Credentials))
	for _, credential := range input.Credentials {
		group := snapshot.GroupCatalog[credential.GroupID]
		view := CredentialConcurrency{Subject: concurrency.Credential(credential.ID), GroupID: credential.GroupID, IdentityGeneration: credential.IdentityGeneration}
		if group.ConnectionType == connection.Subscription && credential.IdentityFingerprint != "" {
			view.Subject = concurrency.Account(string(group.ChannelID), credential.IdentityFingerprint)
			view.Shared = true
		}
		snapshot.CredentialConcurrency[credential.ID] = view
	}
	return nil
}

func (s *ConfigSnapshot) ConcurrencyLimit(subject, fallback concurrency.Subject) int64 {
	if value, exists := s.ConcurrencyPolicies[subject]; exists {
		return value
	}
	return s.ConcurrencyPolicies[fallback]
}

func (s *ConfigSnapshot) RequestConcurrencyLimits(keyID uint) []concurrency.Limit {
	key := concurrency.AccessKey(keyID)
	return []concurrency.Limit{
		{Subject: key, Maximum: s.ConcurrencyLimit(key, concurrency.DefaultAccessKey)},
		{Subject: concurrency.Global, Maximum: s.ConcurrencyPolicies[concurrency.Global]},
	}
}

func (s *ConfigSnapshot) UpstreamConcurrencyLimits(ref CredentialRef) []concurrency.Limit {
	group, credential := concurrency.Group(ref.GroupID), concurrency.Credential(ref.ID)
	if view, exists := s.CredentialConcurrency[ref.ID]; exists {
		credential = view.Subject
	}
	return []concurrency.Limit{
		{Subject: group, Maximum: s.ConcurrencyLimit(group, concurrency.DefaultGroup)},
		{Subject: credential, Maximum: s.ConcurrencyLimit(credential, concurrency.DefaultCredential)},
		{Subject: concurrency.Upstream},
	}
}

// AdmitConcurrency serializes admission with configuration publication. Limits
// are always read from the current snapshot, while counters survive publication.
// Lock order is publishMu.RLock -> concurrency runtime; leases hold neither lock.
func (m *Manager) AdmitConcurrency(snapshot *ConfigSnapshot, limits []concurrency.Limit) (*concurrency.Lease, concurrency.Subject, bool) {
	m.publishMu.RLock()
	defer m.publishMu.RUnlock()
	current := m.current.Load()
	if snapshot == nil || current == nil {
		return nil, "", false
	}
	resolved := make([]concurrency.Limit, len(limits))
	for index := range limits {
		fallback := concurrency.Subject("")
		subject := limits[index].Subject
		switch {
		case strings.HasPrefix(string(subject), "group:"):
			fallback = concurrency.DefaultGroup
		case strings.HasPrefix(string(subject), "access_key:"):
			fallback = concurrency.DefaultAccessKey
		case strings.HasPrefix(string(subject), "credential:"), strings.HasPrefix(string(subject), "account:"):
			fallback = concurrency.DefaultCredential
		}
		resolved[index] = concurrency.Limit{Subject: subject, Maximum: current.ConcurrencyLimit(subject, fallback)}
	}
	lease, blocked := m.concurrency.TryAcquire(resolved...)
	return lease, blocked, true
}

func (m *Manager) ObserveConcurrency(subjects []concurrency.Subject) map[concurrency.Subject]int64 {
	return m.concurrency.Observe(subjects)
}

// AdmitUpstreamConcurrency also covers control-plane probes. Busy probes are
// skipped without recording a credential failure.
func (m *Manager) AdmitUpstreamConcurrency(ref CredentialRef) (*concurrency.Lease, bool) {
	m.publishMu.RLock()
	defer m.publishMu.RUnlock()
	snapshot := m.current.Load()
	if snapshot == nil {
		return nil, false
	}
	if view, exists := snapshot.CredentialConcurrency[ref.ID]; !exists ||
		view.GroupID != ref.GroupID || view.IdentityGeneration != ref.IdentityGeneration {
		return nil, false
	}
	lease, blocked := m.concurrency.TryAcquire(snapshot.UpstreamConcurrencyLimits(ref)...)
	return lease, blocked == ""
}
