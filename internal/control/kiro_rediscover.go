package control

import (
	"context"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// storedKiroAccountID decodes the stored credential and returns the Kiro
// AccountID it currently holds. The account-id extraction is delegated to the
// Kiro subscription provider (which owns the CPA bridge) through a narrow
// runtime interface, keeping the CPA bridge behind the provider boundary.
func (s *Service) storedKiroAccountID(group models.Group, credential models.Credential) string {
	canonical, _, err := s.decodeCredential(group, credential)
	if err != nil || len(canonical) == 0 {
		return ""
	}
	defer clear(canonical)
	driver, err := s.subscriptionDriver(channelIDKiro)
	if err != nil {
		return ""
	}
	identifier, ok := driver.(subscriptionruntime.AccountIdentifier)
	if !ok {
		return ""
	}
	return identifier.StoredAccountID(canonical)
}

// KiroRediscoverResult reports the outcome of an explicit "re-read the local
// Kiro account and swap it into this credential" user action.
type KiroRediscoverResult struct {
	// Swapped is true when a different local account was found and written into
	// the credential row in place.
	Swapped bool `json:"swapped"`
	// Reason classifies when no swap happened:
	//   "no_local_account"     — the local Kiro token cache held no usable account.
	//   "same_account"         — the discovered account identity matches the current one.
	//   ""                     — the swap completed.
	Reason string `json:"reason,omitempty"`
	// PreviousAccount is the account identity stored on the credential before this action.
	PreviousAccount string `json:"previous_account,omitempty"`
	// CurrentAccount is the account identity now present on the credential.
	CurrentAccount string `json:"current_account,omitempty"`
	// ErrorCode mirrors a failed upstream prepare so the UI can show why the
	// credential could not be used afterward.
	ErrorCode string `json:"error_code,omitempty"`
}

// RediscoverKiroCredential is the manual counterpart of the background rotation
// monitor: after the user switches the Kiro desktop app to a different account,
// this re-reads the local token cache and swaps the stored credential onto the
// freshly signed-in account in place. It never runs interactive authorization
// and reports whether a change actually happened.
func (s *Service) RediscoverKiroCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (KiroRediscoverResult, error) {
	if groupID == 0 || credentialID == 0 {
		return KiroRediscoverResult{}, app_errors.ErrBadRequest
	}
	group, credential, err := s.loadSubscriptionCredentialTarget(ctx, groupID, credentialID)
	if err != nil {
		return KiroRediscoverResult{}, err
	}
	if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription ||
		channel.ID(group.ChannelID) != channelIDKiro {
		return KiroRediscoverResult{}, app_errors.ErrValidation
	}
	var result KiroRediscoverResult
	result.PreviousAccount = s.storedKiroAccountID(group, credential)
	result.CurrentAccount = result.PreviousAccount

	discoverer, err := s.subscriptionDriver(channelIDKiro)
	if err != nil {
		return KiroRediscoverResult{}, app_errors.ErrInternalServer
	}
	fresh, found, err := s.discoverLocalKiroAccount(ctx, group, credential, discoverer)
	if err != nil {
		return KiroRediscoverResult{}, err
	}
	if result.finish(fresh, found) {
		return result, nil
	}
	swapped, err := s.swapKiroCredentialToFresh(ctx, group, credential, fresh)
	if err != nil {
		return KiroRediscoverResult{}, err
	}
	if !swapped {
		result.Reason = "no_local_account"
		return result, nil
	}
	result.Swapped = true
	result.CurrentAccount = fresh.Identity()
	s.rotationMonitor.logger.WithField("event", "kiro.rediscover.swapped").
		WithField("credential_id", credentialID).
		WithField("group_id", groupID).
		WithField("previous", result.PreviousAccount).
		WithField("current", result.CurrentAccount).
		Info("Kiro credential re-discovered a fresh local account manually")
	return result, nil
}

// finish applies the no-op classifications (no local account, or the same
// account is still signed in) and reports whether the action is complete.
func (result *KiroRediscoverResult) finish(fresh subscriptionruntime.Credential, found bool) bool {
	if !found {
		result.Reason = "no_local_account"
		result.CurrentAccount = result.PreviousAccount
		return true
	}
	identity := fresh.Identity()
	if identity == "" || identity == result.PreviousAccount {
		result.Reason = "same_account"
		result.CurrentAccount = result.PreviousAccount
		return true
	}
	return false
}
