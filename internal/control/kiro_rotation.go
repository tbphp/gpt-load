package control

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// channelIDKiro is the channel identifier used to select active Kiro
// subscription groups. Kiro's channel ID constant is channel.Kiro.
var channelIDKiro = channel.Kiro

const (
	// kiroRotationInterval controls how often the background rotation monitor
	// re-observes active Kiro subscription credentials. A rotation is only
	// attempted once account usage reaches kiroRotationThreshold.
	kiroRotationInterval = 5 * time.Minute
	// kiroRotationThreshold is the account usage ratio (0..1) at which an
	// explicitly logged-in Kiro account is considered exhausted and eligible
	// for rotation. Set to 0.95 so a fresh account is swapped in just before
	// the account is actually hard-limited at 100%.
	kiroRotationThreshold = 0.95
	// kiroRotationCooldown prevents the monitor from hammering a credential
	// that just came from a rotation (or whose local account did not change)
	// by skipping it for this long after an attempt.
	kiroRotationCooldown = 30 * time.Minute
	// kiroRotationMaxPerCycle bounds how many credentials one cycle processes
	// so a single cycle cannot hold many in-flight upstream observations.
	kiroRotationMaxPerCycle = 8
)

// kiroRotationState is process-local bookkeeping for one credential's rotation.
type kiroRotationState struct {
	lastAttempt time.Time
}

// kiroRotationMonitor drives the background Kiro account rotation loop.
type kiroRotationMonitor struct {
	logger *logrus.Logger
	mu     sync.Mutex
	byID   map[uint]kiroRotationState
}

func newKiroRotationMonitor(logger *logrus.Logger) *kiroRotationMonitor {
	if logger == nil {
		logger = logrus.StandardLogger()
	}
	return &kiroRotationMonitor{logger: logger, byID: make(map[uint]kiroRotationState)}
}

func (monitor *kiroRotationMonitor) shouldAttempt(id uint, cooldown time.Duration, now time.Time) bool {
	if monitor == nil {
		return true
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	state, seen := monitor.byID[id]
	if !seen {
		return true
	}
	return now.Sub(state.lastAttempt) >= cooldown
}

func (monitor *kiroRotationMonitor) noteAttempt(id uint, now time.Time) {
	if monitor == nil {
		return
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	monitor.byID[id] = kiroRotationState{lastAttempt: now}
}

// RunKiroRotation observes active Kiro subscription credentials and rotates
// any whose local account usage has reached the threshold onto a re-discovered
// local Kiro account. It follows the same ticker+context pattern as the other
// background service loops and blocks until ctx is canceled.
func (s *Service) RunKiroRotation(ctx context.Context) {
	if s == nil || s.rotationMonitor == nil {
		return
	}
	ticker := time.NewTicker(kiroRotationInterval)
	defer ticker.Stop()
	s.rotateKiroSubscriptionCredentials(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.rotateKiroSubscriptionCredentials(ctx)
		}
	}
}

// rotateKiroSubscriptionCredentials runs one bounded rotation pass over active
// Kiro subscription groups.
func (s *Service) rotateKiroSubscriptionCredentials(ctx context.Context) {
	groups, err := s.loadActiveKiroSubscriptionGroups(ctx)
	if err != nil {
		s.logRotationFailure("load_active_kiro_groups", 0, err)
		return
	}
	attempted := 0
	for _, group := range groups {
		if attempted >= kiroRotationMaxPerCycle {
			return
		}
		credentials, err := s.loadActiveGroupCredentials(ctx, group.ID)
		if err != nil {
			s.logRotationFailure("load_group_credentials", group.ID, err)
			continue
		}
		for _, credential := range credentials {
			if attempted >= kiroRotationMaxPerCycle {
				return
			}
			if !s.rotationMonitor.shouldAttempt(credential.ID, kiroRotationCooldown, s.now()) {
				continue
			}
			s.rotationMonitor.noteAttempt(credential.ID, s.now())
			attempted++
			s.observeAndRotateKiroCredential(ctx, group, credential)
		}
	}
	s.rotationMonitor.logger.WithField("event", "kiro.rotation.cycle_complete").
		WithField("attempted", attempted).
		Debug("Kiro rotation cycle complete")
}

func (s *Service) loadActiveKiroSubscriptionGroups(ctx context.Context) ([]models.Group, error) {
	var groups []models.Group
	err := s.db.WithContext(ctx).
		Where("connection_type = ? AND channel_id = ? AND enabled = ?",
			models.ConnectionTypeSubscription, channelIDKiro, true).
		Find(&groups).Error
	if err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return groups, nil
}

func (s *Service) loadActiveGroupCredentials(ctx context.Context, groupID uint) ([]models.Credential, error) {
	var rows []models.Credential
	err := s.db.WithContext(ctx).
		Where("group_id = ? AND status = ? AND auth_state = ?",
			groupID, models.CredentialStatusActive, models.CredentialAuthStateReady).
		Find(&rows).Error
	if err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return rows, nil
}

// observeAndRotateKiroCredential observes credential's live account usage and,
// when it has reached the rotation threshold, attempts to re-discover a fresh
// local Kiro account and swap it in place if the account identity changed.
func (s *Service) observeAndRotateKiroCredential(ctx context.Context, group models.Group, credential models.Credential) {
	credentialID := credential.ID
	if s.credentialObservationRefreshInFlight(group.ID, credentialID) {
		return
	}
	prepared, observation, err := s.observeKiroCredential(ctx, group, credential)
	if err != nil {
		s.logRotationFailure("observe", credentialID, err)
		return
	}
	usage, observed := kiroPrimaryQuotaUsage(observation)
	if !observed {
		s.logRotationInfo(credentialID, "observed", 0, nil)
		return
	}
	if usage < kiroRotationThreshold {
		s.rotationMonitor.logger.WithField("event", "kiro.rotation.skipped_below_threshold").
			WithField("credential_id", credentialID).WithField("usage", usage).
			Debug("Kiro account usage is below rotation threshold")
		return
	}
	rotated, err := s.rotateKiroCredential(ctx, group, credential, prepared)
	switch {
	case err != nil:
		s.logRotationFailure("rotate", credentialID, err)
	case rotated:
		s.logRotationInfo(credentialID, "rotated", usage, nil)
	default:
		s.rotationMonitor.logger.WithField("event", "kiro.rotation.no_new_account").
			WithField("credential_id", credentialID).WithField("usage", usage).
			Warn("Kiro account usage at rotation threshold but no different local account found; skipped")
	}
}

// observeKiroCredential prepares a usable credential and reads its live quota.
func (s *Service) observeKiroCredential(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
) (subscriptionruntime.Credential, subscriptionruntime.Observation, error) {
	network, err := s.credentialNetworkContext(ctx, s.db, group, credential)
	if err != nil {
		return subscriptionruntime.Credential{}, subscriptionruntime.Observation{}, err
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	prepared, err := s.prepareStoredSubscriptionCredential(ctx, group, credential)
	if err != nil {
		return subscriptionruntime.Credential{}, subscriptionruntime.Observation{}, err
	}
	if s.observeSubscriptionAccount == nil {
		return subscriptionruntime.Credential{}, subscriptionruntime.Observation{}, app_errors.ErrInternalServer
	}
	observeContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	observation, err := s.observeSubscriptionAccount(observeContext, channel.ID(group.ChannelID), prepared)
	if err != nil {
		return subscriptionruntime.Credential{}, subscriptionruntime.Observation{}, err
	}
	return prepared, observation, nil
}

// rotateKiroCredential re-discovers a local Kiro account and, when its identity
// differs from the current credential, swaps the exhausted credential's secret
// and identity in place on the same row. It reports whether a rotation happened.
func (s *Service) rotateKiroCredential(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
	current subscriptionruntime.Credential,
) (bool, error) {
	discoverer, err := s.subscriptionDriver(channel.ID(group.ChannelID))
	if err != nil {
		return false, err
	}
	fresh, found, err := s.discoverLocalKiroAccount(ctx, group, credential, discoverer)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	freshIdentity := fresh.Identity()
	if freshIdentity == "" || freshIdentity == current.Identity() {
		return false, nil
	}
	swapped, err := s.swapKiroCredentialToFresh(ctx, group, credential, fresh)
	if !swapped || err != nil {
		return swapped, err
	}
	s.rotationMonitor.logger.WithField("event", "kiro.rotation.swapped").
		WithField("credential_id", credential.ID).
		WithField("group_id", group.ID).
		Info("Kiro account rotated onto a fresh local account")
	return true, nil
}

// discoverLocalKiroAccount re-reads the Kiro desktop app's local token cache
// and returns the currently signed-in account credential, without any
// interactive authorization.
func (s *Service) discoverLocalKiroAccount(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
	discoverer subscriptionruntime.Driver,
) (subscriptionruntime.Credential, bool, error) {
	driver, ok := discoverer.(subscriptionruntime.SelfDiscoveryDriver)
	if !ok {
		return subscriptionruntime.Credential{}, false, nil
	}
	network, err := s.credentialNetworkContext(ctx, s.db, group, credential)
	if err != nil {
		return subscriptionruntime.Credential{}, false, err
	}
	discoveryCtx := subscriptionruntime.WithNetworkContext(ctx, network)
	discoveryContext, cancel := context.WithTimeout(discoveryCtx, defaultSubscriptionControlTimeout)
	defer cancel()
	return driver.DiscoverLocalCredential(discoveryContext)
}

// swapKiroCredentialToFresh replaces the stored credential's secret and
// identity in place with a freshly discovered local account, encrypting the
// new canonical value and resynchronizing the runtime registry.
func (s *Service) swapKiroCredentialToFresh(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
	fresh subscriptionruntime.Credential,
) (bool, error) {
	freshIdentity := fresh.Identity()
	if freshIdentity == "" {
		return false, nil
	}
	canonical := fresh.Canonical()
	if len(canonical) == 0 {
		return false, app_errors.ErrInternalServer
	}
	ciphertext, err := s.encryption.Encrypt(string(canonical))
	fingerprint := s.encryption.Hash(string(canonical))
	identityFingerprint := s.subscriptionIdentityFingerprint(channel.ID(group.ChannelID), freshIdentity)
	clear(canonical)
	if err != nil {
		return false, app_errors.ErrInternalServer
	}
	groupID := group.ID
	credentialID := credential.ID
	err = s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		var row models.Credential
		if err := tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return credentialNotFoundError()
			}
			return app_errors.ParseDBError(err)
		}
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription ||
			channel.ID(group.ChannelID) != channelIDKiro {
			return app_errors.ErrValidation
		}
		return tx.Model(&models.Credential{}).
			Where("id = ? AND group_id = ? AND secret_version = ?", credentialID, groupID, row.SecretVersion).
			Updates(map[string]any{
				"data":                 ciphertext,
				"fingerprint":          fingerprint,
				"identity_fingerprint": identityFingerprint,
				"secret_version":       row.SecretVersion + 1,
				"auth_state":           models.CredentialAuthStateReady,
				"auth_error_code":      "",
				"updated_at_ms":        s.now().UnixMilli(),
			}).Error
	}, func() error {
		entries, snapshotErr := s.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credentialID})
		if snapshotErr != nil {
			return snapshotErr
		}
		entry := entries[0]
		entry.Version = groupCollectionCredentialVersion(credential.SecretVersion + 1)
		entry.IdentityGeneration = groupCollectionCredentialIdentity(
			identityFingerprint,
			group,
		)
		entry.Fingerprint = fingerprint
		entry.EncryptedValue = ciphertext
		if err := s.registry.RestoreGroupCredentialEntriesExact(groupID, []state.CredentialEntry{entry}); err != nil {
			return err
		}
		s.retireCredentialRuntime(credentialID)
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// kiroPrimaryQuotaUsage returns the primary quota window's utilization ratio
// (0..1) and whether any window carried an authoritative usage reading.
func kiroPrimaryQuotaUsage(observation subscriptionruntime.Observation) (float64, bool) {
	if len(observation.Payload) == 0 {
		return 0, false
	}
	var snapshot CredentialObservationSnapshot
	if err := json.Unmarshal(observation.Payload, &snapshot); err != nil || snapshot.QuotaWindows == nil {
		return 0, false
	}
	for _, window := range snapshot.QuotaWindows {
		if window.Utilization == nil && window.Limit == nil {
			continue
		}
		ratio := 0.0
		switch {
		case window.Utilization != nil:
			ratio = *window.Utilization
		case window.Limit != nil && *window.Limit > 0 && window.Used != nil:
			ratio = *window.Used / *window.Limit
		}
		if window.IsPrimary || ratio >= kiroRotationThreshold {
			return ratio, true
		}
	}
	return 0, false
}

func (s *Service) logRotationFailure(stage string, credentialID uint, err error) {
	s.rotationMonitor.logger.WithField("event", "kiro.rotation.failed").
		WithField("stage", stage).
		WithField("credential_id", credentialID).
		WithError(err).
		Warn("Kiro account rotation step failed")
}

func (s *Service) logRotationInfo(credentialID uint, action string, usage float64, _ error) {
	s.rotationMonitor.logger.WithField("event", "kiro.rotation."+action).
		WithField("credential_id", credentialID).
		WithField("usage", usage).
		Info("Kiro account rotation step completed")
}
