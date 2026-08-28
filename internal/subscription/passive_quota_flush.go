package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

// passiveQuotaFlushBatchSize bounds how many credentials one
// FlushPassiveQuotaObservations call processes, matching the "one bounded
// batch per worker cycle" contract shared with RequestLog's own checkpoints.
const passiveQuotaFlushBatchSize = 20

// FlushPassiveQuotaObservations persists up to one bounded batch of pending
// passive quota observations into credential_observations and reports
// whether pending observations still remain for another wake-up. Each
// credential is its own independent transaction-free conditional update: it
// only ever touches snapshot_json, observed_at_ms, and updated_at_ms. State,
// plan/account summaries, reset credits, and the most recent active
// attempt/error metadata are always left untouched. A credential with no
// existing observation row, a stale identity generation, or no window ID
// the row already tracks is discarded without writing anything, since manual
// or automatic active observation remains the sole owner of window creation.
func (manager *CredentialManager) FlushPassiveQuotaObservations(ctx context.Context) (bool, error) {
	if manager == nil || manager.passiveQuota == nil || manager.db == nil {
		return false, nil
	}
	batch := manager.passiveQuota.dirtyObservations(passiveQuotaFlushBatchSize)
	for _, observation := range batch {
		if err := manager.flushOnePassiveQuotaObservation(ctx, observation); err != nil {
			return true, err
		}
	}
	return len(manager.passiveQuota.dirtyObservations(1)) > 0, nil
}

func (manager *CredentialManager) flushOnePassiveQuotaObservation(
	ctx context.Context,
	observation PassiveQuotaObservation,
) error {
	if manager.registry == nil {
		return nil
	}
	ref, ok := manager.registry.CredentialRef(observation.CredentialID)
	if !ok || ref.IdentityGeneration != observation.IdentityGeneration {
		manager.passiveQuota.ack(observation.CredentialID, observation.Version)
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		var row models.CredentialObservation
		err := manager.db.WithContext(ctx).Take(&row, "credential_id = ?", observation.CredentialID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				manager.passiveQuota.ack(observation.CredentialID, observation.Version)
				return nil
			}
			return fmt.Errorf("read credential observation %d: %w", observation.CredentialID, err)
		}
		if row.ObservedAtMS != nil && observation.ObservedAtMS < *row.ObservedAtMS {
			// A newer observation -- typically a manual refresh -- was persisted
			// while this sample sat in the pending map. Writing it now would both
			// rewind observed_at_ms and overwrite newer quota values with older
			// ones, so the sample is dropped instead. The updated_at_ms guard
			// below only catches writes that land after this read.
			manager.passiveQuota.ack(observation.CredentialID, observation.Version)
			return nil
		}
		merge, mergeErr := mergePassiveQuotaSnapshot(row.SnapshotJSON, observation.Windows)
		if mergeErr != nil {
			// A snapshot this malformed cannot be repaired by retrying the
			// same merge; drop the observation instead of retrying forever.
			manager.passiveQuota.ack(observation.CredentialID, observation.Version)
			return nil
		}
		alreadyObservedAt := row.ObservedAtMS != nil && *row.ObservedAtMS == observation.ObservedAtMS
		if !merge.Matched || (!merge.Changed && alreadyObservedAt) {
			// Nothing this response says is new: either it named no window the
			// snapshot tracks, or it repeated values already stored at this
			// very instant.
			manager.passiveQuota.ack(observation.CredentialID, observation.Version)
			return nil
		}
		// A matched window whose values did not move is still evidence the
		// credential was observed just now, so the sync time advances even
		// when the snapshot itself stays byte-identical.
		updates := map[string]any{
			"observed_at_ms": observation.ObservedAtMS,
			"updated_at_ms":  manager.now().UnixMilli(),
		}
		if merge.Changed {
			updates["snapshot_json"] = models.JSON(merge.Encoded)
		}
		result := manager.db.WithContext(ctx).Model(&models.CredentialObservation{}).
			Where("credential_id = ? AND updated_at_ms = ?", observation.CredentialID, row.UpdatedAtMS).
			Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("update credential observation %d: %w", observation.CredentialID, result.Error)
		}
		if result.RowsAffected == 1 {
			manager.passiveQuota.ack(observation.CredentialID, observation.Version)
			if row.State == models.CredentialObservationFresh {
				manager.registry.ApplyQuotaWindows(observation.CredentialID, merge.Windows)
			}
			return nil
		}
		// An active refresh or a reset committed between our read and our
		// write. Retry once against the row it left behind.
	}
	return fmt.Errorf("passive quota observation for credential %d conflicted with a concurrent write", observation.CredentialID)
}

// passiveQuotaMerge is the outcome of overlaying one response's windows onto
// a stored snapshot. Matched and Changed are distinct on purpose: a response
// that names a tracked window but repeats its values is still a fresh
// observation of that credential, while a response naming no tracked window
// is no observation of this snapshot at all.
type passiveQuotaMerge struct {
	Encoded []byte
	Windows []providerobservation.QuotaWindow
	Matched bool
	Changed bool
}

// mergePassiveQuotaSnapshot overlays patches onto the quota_windows array
// inside a stored snapshot, carrying every other field through unchanged by
// value. The snapshot is decoded and re-encoded, so key order and formatting
// are not preserved byte-for-byte. Only window IDs the snapshot already
// tracks are updated; an unmatched patch ID is silently ignored so a passive
// signal never creates a window.
func mergePassiveQuotaSnapshot(
	raw []byte,
	patches []providerobservation.QuotaWindow,
) (result passiveQuotaMerge, err error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return passiveQuotaMerge{}, fmt.Errorf("decode credential observation snapshot: %w", err)
	}
	windowsRaw, ok := fields["quota_windows"]
	if !ok {
		return passiveQuotaMerge{}, errors.New("credential observation snapshot has no quota_windows field")
	}
	var existing []providerobservation.QuotaWindow
	if err := json.Unmarshal(windowsRaw, &existing); err != nil {
		return passiveQuotaMerge{}, fmt.Errorf("decode credential observation quota windows: %w", err)
	}
	merged := append([]providerobservation.QuotaWindow(nil), existing...)
	index := make(map[string]int, len(merged))
	for position, window := range merged {
		index[window.ID] = position
	}
	outcome := passiveQuotaMerge{Encoded: raw, Windows: existing}
	for _, patch := range patches {
		position, exists := index[patch.ID]
		if !exists {
			continue
		}
		outcome.Matched = true
		next := providerobservation.MergeQuotaWindow(merged[position], patch)
		if !reflect.DeepEqual(next, merged[position]) {
			outcome.Changed = true
		}
		merged[position] = next
	}
	if !outcome.Changed {
		return outcome, nil
	}
	encodedWindows, err := json.Marshal(merged)
	if err != nil {
		return passiveQuotaMerge{}, fmt.Errorf("encode credential observation quota windows: %w", err)
	}
	fields["quota_windows"] = encodedWindows
	encoded, err := json.Marshal(fields)
	if err != nil {
		return passiveQuotaMerge{}, fmt.Errorf("encode credential observation snapshot: %w", err)
	}
	outcome.Encoded, outcome.Windows = encoded, merged
	return outcome, nil
}
