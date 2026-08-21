package control

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

const healthResetCreditWarningWindow = 48 * time.Hour

type healthResetCreditTarget struct {
	groupID   uint
	groupName string
}

func (service *Service) loadExpiringResetCredits(
	targets map[uint]healthResetCreditTarget,
	now time.Time,
) ([]healthExpiringResetCreditResponse, error) {
	result := []healthExpiringResetCreditResponse{}
	if len(targets) == 0 {
		return result, nil
	}
	if service == nil || service.db == nil || !service.db.Migrator().HasTable(&models.CredentialObservation{}) {
		return result, nil
	}
	credentialIDs := make([]uint, 0, len(targets))
	for credentialID := range targets {
		credentialIDs = append(credentialIDs, credentialID)
	}
	var observations []models.CredentialObservation
	if err := service.db.Select(
		"credential_id", "snapshot_json", "state",
	).Where(
		"credential_id IN ? AND state = ?", credentialIDs, models.CredentialObservationFresh,
	).Find(&observations).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	nowMS := now.UTC().UnixMilli()
	warningUntilMS := now.Add(healthResetCreditWarningWindow).UTC().UnixMilli()
	for _, observation := range observations {
		target, exists := targets[observation.CredentialID]
		if !exists {
			continue
		}
		var snapshot CredentialObservationSnapshot
		if err := json.Unmarshal(observation.SnapshotJSON, &snapshot); err != nil {
			return nil, fmt.Errorf(
				"decode reset credit observation for credential %d: %w",
				observation.CredentialID,
				app_errors.ErrInternalServer,
			)
		}
		count := 0
		nearest := int64(0)
		for _, credit := range snapshot.ResetCredits {
			if credit.ExpiresAtMS == nil || *credit.ExpiresAtMS <= nowMS ||
				*credit.ExpiresAtMS > warningUntilMS {
				continue
			}
			count++
			if nearest == 0 || *credit.ExpiresAtMS < nearest {
				nearest = *credit.ExpiresAtMS
			}
		}
		if count == 0 {
			continue
		}
		result = append(result, healthExpiringResetCreditResponse{
			CredentialID: observation.CredentialID,
			GroupID:      target.groupID, GroupName: target.groupName,
			Count: count, NearestExpiresAtMS: nearest,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].NearestExpiresAtMS != result[j].NearestExpiresAtMS {
			return result[i].NearestExpiresAtMS < result[j].NearestExpiresAtMS
		}
		return result[i].CredentialID < result[j].CredentialID
	})
	return result, nil
}
