package control

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

const (
	credentialObservationSweepInterval = 5 * time.Minute
	credentialObservationRefreshLead   = 10 * time.Minute
)

type credentialObservationRefreshTarget struct {
	groupID      uint
	credentialID uint
}

func (s *Service) RunCredentialObservationRefresh(ctx context.Context) {
	if s == nil || s.observeCodexAccount == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.refreshDueCredentialObservations(ctx)
	ticker := time.NewTicker(credentialObservationSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshDueCredentialObservations(ctx)
		}
	}
}

func (s *Service) refreshDueCredentialObservations(ctx context.Context) {
	if s == nil || s.observeCodexAccount == nil || ctx.Err() != nil {
		return
	}
	now := s.now().UTC()
	targets, err := s.dueCredentialObservationTargets(ctx, now)
	if err != nil {
		logrus.WithError(err).WithField("event", "credential_observation_sweep").Warn(
			"subscription observation sweep failed",
		)
		return
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.refreshCredentialObservation(
			ctx,
			target.groupID,
			target.credentialID,
			observationRefreshScheduled,
		); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"event":         "credential_observation_refresh",
				"group_id":      target.groupID,
				"credential_id": target.credentialID,
			}).Debug("subscription observation refresh deferred")
		}
	}
}

func (s *Service) dueCredentialObservationTargets(
	ctx context.Context,
	now time.Time,
) ([]credentialObservationRefreshTarget, error) {
	var groupIDs []uint
	if err := s.db.WithContext(ctx).Model(&models.Group{}).
		Where("channel_id = ? AND connection_type = ? AND enabled = ?", channel.Codex, models.ConnectionTypeSubscription, true).
		Order("id ASC").
		Pluck("id", &groupIDs).Error; err != nil || len(groupIDs) == 0 {
		return nil, err
	}
	var credentials []models.Credential
	if err := s.db.WithContext(ctx).
		Where("group_id IN ? AND status = ? AND auth_state = ?", groupIDs, models.CredentialStatusActive, models.CredentialAuthStateReady).
		Order("group_id ASC").
		Order("id ASC").
		Find(&credentials).Error; err != nil || len(credentials) == 0 {
		return nil, err
	}
	ids := make([]uint, len(credentials))
	for index := range credentials {
		ids[index] = credentials[index].ID
	}
	var observations []models.CredentialObservation
	if err := s.db.WithContext(ctx).Where("credential_id IN ?", ids).Find(&observations).Error; err != nil {
		return nil, err
	}
	byCredential := make(map[uint]models.CredentialObservation, len(observations))
	for _, observation := range observations {
		byCredential[observation.CredentialID] = observation
	}
	nowMS := now.UnixMilli()
	refreshBeforeMS := now.Add(credentialObservationRefreshLead).UnixMilli()
	targets := make([]credentialObservationRefreshTarget, 0, len(credentials))
	for _, credential := range credentials {
		observation, exists := byCredential[credential.ID]
		identityMatches := exists && observation.IdentityFingerprint == credential.IdentityFingerprint
		if identityMatches && observation.NextAllowedAtMS != nil && *observation.NextAllowedAtMS > nowMS {
			continue
		}
		if identityMatches && observation.State == models.CredentialObservationFresh &&
			observation.FreshUntilMS != nil && *observation.FreshUntilMS > refreshBeforeMS {
			continue
		}
		targets = append(targets, credentialObservationRefreshTarget{
			groupID: credential.GroupID, credentialID: credential.ID,
		})
	}
	return targets, nil
}
