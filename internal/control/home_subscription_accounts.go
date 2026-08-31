package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

const (
	homeSubscriptionAccountLimit  = 4
	homeSubscriptionAccountWindow = 24 * time.Hour
)

type HomeSubscriptionAccountsResponse struct {
	ObservedAtMS int64                             `json:"observed_at_ms"`
	Items        []HomeSubscriptionAccountResponse `json:"items"`
}

type HomeSubscriptionAccountResponse struct {
	ChannelID           string                       `json:"channel_id"`
	ChannelName         string                       `json:"channel_name"`
	ChannelMark         string                       `json:"channel_mark"`
	ChannelIcon         string                       `json:"channel_icon"`
	Capabilities        channel.CapabilityDescriptor `json:"capabilities"`
	GroupCount          int                          `json:"group_count"`
	AvailableGroupCount int                          `json:"available_group_count"`
	Credential          CredentialItemResponse       `json:"credential"`
}

type homeSubscriptionActivityRow struct {
	IdentityFingerprint string `gorm:"column:identity_fingerprint"`
	ChannelID           string `gorm:"column:channel_id"`
	SuccessCount        int64  `gorm:"column:success_count"`
	LastSuccessAtMS     int64  `gorm:"column:last_success_at_ms"`
}

type homeSubscriptionRows struct {
	activity     []homeSubscriptionActivityRow
	credentials  []models.Credential
	observations []models.CredentialObservation
}

type homeSubscriptionMembership struct {
	credential  models.Credential
	observation models.CredentialObservation
	view        state.CredentialRuntimeView
	bucket      healthBucket
}

func (s *Service) ReadHomeSubscriptionAccounts(
	ctx context.Context,
) (HomeSubscriptionAccountsResponse, error) {
	if s == nil || s.db == nil || s.registry == nil || s.registrySnapshot == nil ||
		s.stats == nil || s.channelRegistry == nil || s.encryption == nil || s.now == nil {
		return HomeSubscriptionAccountsResponse{}, app_errors.ErrInternalServer
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return HomeSubscriptionAccountsResponse{}, err
	}
	return s.readHomeSubscriptionAccounts(ctx, s.now().UTC())
}

func (s *Server) handleHomeSubscriptionAccounts(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "home_subscription_accounts", app_errors.ErrBadRequest)
		return
	}
	result, err := s.service.ReadHomeSubscriptionAccounts(c.Request.Context())
	if err != nil {
		writeServiceError(c, "home_subscription_accounts", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Service) readHomeSubscriptionAccounts(
	ctx context.Context,
	observedAt time.Time,
) (HomeSubscriptionAccountsResponse, error) {
	observedAtMS, err := safeEpochMilliseconds(observedAt)
	if err != nil {
		return HomeSubscriptionAccountsResponse{}, err
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	rows, err := s.readHomeSubscriptionRows(ctx, observedAt)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return HomeSubscriptionAccountsResponse{}, contextErr
		}
		var apiErr *app_errors.APIError
		if errors.As(err, &apiErr) {
			return HomeSubscriptionAccountsResponse{}, err
		}
		return HomeSubscriptionAccountsResponse{}, app_errors.ParseDBError(err)
	}
	if len(rows.activity) == 0 {
		return HomeSubscriptionAccountsResponse{
			ObservedAtMS: observedAtMS,
			Items:        []HomeSubscriptionAccountResponse{},
		}, nil
	}

	runtimeByID := make(map[uint]state.CredentialRuntimeView)
	for _, view := range s.registrySnapshot() {
		runtimeByID[view.ID] = view
	}
	observationByID := make(map[uint]models.CredentialObservation, len(rows.observations))
	for _, observation := range rows.observations {
		observationByID[observation.CredentialID] = observation
	}
	memberships := make(map[string][]homeSubscriptionMembership, len(rows.activity))
	for _, credential := range rows.credentials {
		if credential.Group == nil ||
			normalizeGroupConnectionType(credential.Group.ConnectionType) != models.ConnectionTypeSubscription {
			return HomeSubscriptionAccountsResponse{}, app_errors.ErrInternalServer
		}
		view, exists := runtimeByID[credential.ID]
		if !exists || view.GroupID != credential.GroupID ||
			view.Status != state.CredentialStatus(credential.Status) ||
			view.AuthState != normalizeRuntimeCredentialAuthState(credential.AuthState) ||
			view.Version != groupCollectionCredentialVersion(credential.SecretVersion) ||
			view.IdentityGeneration != groupCollectionCredentialIdentity(
				credential.IdentityFingerprint,
				*credential.Group,
			) {
			return HomeSubscriptionAccountsResponse{}, app_errors.ErrInternalServer
		}
		catalog := state.GroupCatalogView{
			ID: credential.Group.ID, Name: credential.Group.Name,
			Enabled: credential.Group.Enabled, WeightManual: cloneInt(credential.Group.WeightManual),
		}
		key := homeSubscriptionIdentityKey(
			credential.Group.ChannelID,
			credential.IdentityFingerprint,
		)
		memberships[key] = append(memberships[key], homeSubscriptionMembership{
			credential:  credential,
			observation: observationByID[credential.ID],
			view:        view,
			bucket:      classifyHealthKey(catalog, view, observedAt),
		})
	}

	items := make([]HomeSubscriptionAccountResponse, 0, len(rows.activity))
	for _, activity := range rows.activity {
		key := homeSubscriptionIdentityKey(activity.ChannelID, activity.IdentityFingerprint)
		accountMemberships := memberships[key]
		if len(accountMemberships) == 0 || activity.SuccessCount < 1 {
			return HomeSubscriptionAccountsResponse{}, app_errors.ErrInternalServer
		}
		item, err := s.mapHomeSubscriptionAccount(ctx, accountMemberships, observedAt)
		if err != nil {
			return HomeSubscriptionAccountsResponse{}, err
		}
		descriptor, exists := s.channelRegistry.Get(channel.ID(activity.ChannelID))
		if !exists || descriptor.Connection.Type != string(models.ConnectionTypeSubscription) {
			return HomeSubscriptionAccountsResponse{}, app_errors.ErrInternalServer
		}
		item.ChannelID = activity.ChannelID
		item.ChannelName = descriptor.Name
		item.ChannelMark = descriptor.Mark
		item.ChannelIcon = descriptor.Icon
		item.Capabilities = descriptor.Capabilities
		items = append(items, item)
	}
	return HomeSubscriptionAccountsResponse{ObservedAtMS: observedAtMS, Items: items}, nil
}

func (s *Service) readHomeSubscriptionRows(
	ctx context.Context,
	observedAt time.Time,
) (homeSubscriptionRows, error) {
	rows := homeSubscriptionRows{}
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		activityScope := homeSubscriptionActivityScope(
			tx,
			observedAt.Add(-homeSubscriptionAccountWindow).UnixMilli(),
			observedAt.UnixMilli(),
		)
		if err := activityScope.Find(&rows.activity).Error; err != nil {
			return fmt.Errorf("query home subscription activity: %w", err)
		}
		if len(rows.activity) == 0 {
			return nil
		}
		identities := make([]string, 0, len(rows.activity))
		for _, activity := range rows.activity {
			if activity.IdentityFingerprint == "" || activity.ChannelID == "" ||
				activity.SuccessCount < 1 || validateSafeMilliseconds(activity.LastSuccessAtMS) != nil {
				return fmt.Errorf("validate home subscription activity: %w", app_errors.ErrInternalServer)
			}
			identities = append(identities, activity.IdentityFingerprint)
		}
		subscriptionGroups := tx.Session(&gorm.Session{NewDB: true}).
			Model(&models.Group{}).
			Select("id").
			Where("connection_type = ?", models.ConnectionTypeSubscription)
		if err := tx.Preload("Group").
			Where("identity_fingerprint IN ?", identities).
			Where("group_id IN (?)", subscriptionGroups).
			Order("id ASC").
			Find(&rows.credentials).Error; err != nil {
			return fmt.Errorf("query home subscription credentials: %w", err)
		}
		credentialIDs := make([]uint, 0, len(rows.credentials))
		for _, credential := range rows.credentials {
			credentialIDs = append(credentialIDs, credential.ID)
		}
		if len(credentialIDs) == 0 {
			return nil
		}
		if err := tx.Where("credential_id IN ?", credentialIDs).
			Find(&rows.observations).Error; err != nil {
			return fmt.Errorf("query home subscription observations: %w", err)
		}
		return nil
	})
	return rows, err
}

func homeSubscriptionActivityScope(db *gorm.DB, fromMS, toMS int64) *gorm.DB {
	subscriptionGroups := db.Session(&gorm.Session{NewDB: true}).
		Model(&models.Group{}).
		Select("id, channel_id").
		Where("connection_type = ?", models.ConnectionTypeSubscription)
	return db.Table("credential_attempt_stats").
		Select(
			"credentials.identity_fingerprint, subscription_groups.channel_id, "+
				"SUM(credential_attempt_stats.success_count) AS success_count, "+
				"MAX(credential_attempt_stats.bucket_start_ms) AS last_success_at_ms",
		).
		Joins("JOIN credentials ON credentials.id = credential_attempt_stats.credential_id").
		Joins(
			"JOIN (?) AS subscription_groups ON subscription_groups.id = credentials.group_id",
			subscriptionGroups,
		).
		Where("credential_attempt_stats.bucket_start_ms >= ?", fromMS).
		Where("credential_attempt_stats.bucket_start_ms < ?", toMS).
		Where("credential_attempt_stats.success_count > 0").
		Group("credentials.identity_fingerprint, subscription_groups.channel_id").
		Order("success_count DESC, last_success_at_ms DESC, credentials.identity_fingerprint ASC").
		Limit(homeSubscriptionAccountLimit)
}

func (s *Service) mapHomeSubscriptionAccount(
	ctx context.Context,
	memberships []homeSubscriptionMembership,
	observedAt time.Time,
) (HomeSubscriptionAccountResponse, error) {
	representative := memberships[0]
	available := 0
	for _, membership := range memberships {
		if membership.bucket == healthBucketAvailable {
			available++
		}
		if homeSubscriptionRepresentativeLess(representative, membership) {
			representative = membership
		}
	}
	group := *representative.credential.Group
	canonical, identity, err := s.decodeCredential(group, representative.credential)
	if err != nil {
		return HomeSubscriptionAccountResponse{}, err
	}
	mask, account, err := s.credentialPresentation(
		group,
		representative.credential,
		canonical,
		identity,
	)
	if err != nil {
		return HomeSubscriptionAccountResponse{}, err
	}
	credential := representative.credential
	item, err := mapCredentialRuntimeItem(
		mask,
		credential.ID,
		representative.view,
		representative.bucket,
		s.stats.Snapshot(credential.ID, observedAt),
		observedAt,
	)
	if err != nil {
		return HomeSubscriptionAccountResponse{}, err
	}
	item.ConnectionType = string(models.ConnectionTypeSubscription)
	item.SecretVersion = credential.SecretVersion
	item.AuthState = string(credential.AuthState)
	item.AuthErrorCode = safeInternalErrorCode(credential.AuthErrorCode)
	item.Account = account
	item.Observation = presentCredentialObservation(
		representative.observation,
		credential.IdentityFingerprint,
	)
	proxyViews, err := s.credentialProxyViews(ctx, s.db, group, []models.Credential{credential})
	if err != nil {
		return HomeSubscriptionAccountResponse{}, err
	}
	item.Proxy = proxyViews[credential.ID]
	return HomeSubscriptionAccountResponse{
		GroupCount:          len(memberships),
		AvailableGroupCount: available,
		Credential:          item,
	}, nil
}

func homeSubscriptionRepresentativeLess(
	current homeSubscriptionMembership,
	candidate homeSubscriptionMembership,
) bool {
	currentObservedAt := int64(-1)
	if current.observation.ObservedAtMS != nil &&
		current.observation.IdentityFingerprint == current.credential.IdentityFingerprint {
		currentObservedAt = *current.observation.ObservedAtMS
	}
	candidateObservedAt := int64(-1)
	if candidate.observation.ObservedAtMS != nil &&
		candidate.observation.IdentityFingerprint == candidate.credential.IdentityFingerprint {
		candidateObservedAt = *candidate.observation.ObservedAtMS
	}
	if currentObservedAt != candidateObservedAt {
		return candidateObservedAt > currentObservedAt
	}
	if (current.bucket == healthBucketAvailable) != (candidate.bucket == healthBucketAvailable) {
		return candidate.bucket == healthBucketAvailable
	}
	return candidate.credential.ID < current.credential.ID
}

func homeSubscriptionIdentityKey(channelID, identityFingerprint string) string {
	return channelID + "\x00" + identityFingerprint
}
