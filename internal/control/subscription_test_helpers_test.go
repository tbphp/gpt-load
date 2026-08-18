package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type codexControlTestHooks struct {
	mu      sync.RWMutex
	account func(context.Context, codex.Credential) (codex.AccountObservation, error)
	details func(context.Context, codex.Credential) (codex.AccountObservation, error)
}

var codexControlHooks sync.Map

func installCodexControlTestHooks(service *Service) {
	hooks := &codexControlTestHooks{}
	codexControlHooks.Store(service, hooks)
	service.observeSubscriptionAccount = func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) (subscriptionruntime.Observation, error) {
		if channelID != channel.Codex {
			return subscriptionruntime.Observation{}, errors.New("unexpected subscription channel")
		}
		hooks.mu.RLock()
		account, details := hooks.account, hooks.details
		hooks.mu.RUnlock()
		if account == nil {
			return subscriptionruntime.Observation{}, errors.New("test observation is not configured")
		}
		codexCredential, err := testCodexCredential(credential)
		if err != nil {
			return subscriptionruntime.Observation{}, err
		}
		observed, err := account(ctx, codexCredential)
		if err != nil {
			return subscriptionruntime.Observation{}, err
		}
		snapshot, err := normalizeCodexObservation(observed.Payload)
		if err != nil {
			return subscriptionruntime.Observation{}, fmt.Errorf("%w: %v", subscriptionruntime.ErrObservationPayloadInvalid, err)
		}
		if details != nil {
			detail, detailErr := details(ctx, codexCredential)
			if detailErr == nil {
				count, credits, present, normalizeErr := normalizeCodexResetCreditDetails(detail.Payload)
				if count != nil {
					snapshot.ResetCreditsAvailable = count
				}
				if normalizeErr == nil && present {
					snapshot.ResetCredits = credits
				}
			}
		}
		payload, err := json.Marshal(snapshot)
		return subscriptionruntime.Observation{Payload: payload, Header: observed.Header.Clone()}, err
	}
	service.consumeSubscriptionResetCredit = func(context.Context, channel.ID, subscriptionruntime.Credential, string) (subscriptionruntime.ResetCreditResult, error) {
		return subscriptionruntime.ResetCreditResult{}, errors.New("test reset-credit action is not configured")
	}
}

func setCodexAuthorizationCompletion(
	t *testing.T,
	service *Service,
	complete func(context.Context, codex.BrowserAuthorizationCompletion) (codex.Credential, error),
) {
	t.Helper()
	service.completeSubscriptionAuthorization = func(ctx context.Context, channelID channel.ID, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
		if channelID != channel.Codex {
			return subscriptionruntime.Credential{}, errors.New("unexpected subscription channel")
		}
		var driverState struct {
			Verifier string `json:"verifier"`
		}
		if err := json.Unmarshal(completion.DriverState, &driverState); err != nil {
			return subscriptionruntime.Credential{}, err
		}
		credential, err := complete(ctx, codex.BrowserAuthorizationCompletion{
			ExpectedState: completion.ExpectedState,
			ReturnedState: completion.ReturnedState,
			Code:          completion.Code,
			CodeVerifier:  driverState.Verifier,
		})
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		return testRuntimeCredential(service, credential)
	}
}

func setCodexCredentialRefresh(
	t *testing.T,
	service *Service,
	refresh func(context.Context, codex.Credential) (codex.Credential, error),
) {
	t.Helper()
	service.refreshSubscriptionCredential = func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
		if channelID != channel.Codex {
			return subscriptionruntime.Credential{}, errors.New("unexpected subscription channel")
		}
		current, err := testCodexCredential(credential)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		refreshed, err := refresh(ctx, current)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		return testRuntimeCredential(service, refreshed)
	}
}

func setCodexCredentialPreparer(
	t *testing.T,
	service *Service,
	prepare func(context.Context, execution.CredentialSnapshot) (codex.Credential, *execution.ErrorEvidence),
) {
	t.Helper()
	service.prepareSubscriptionCredential = func(ctx context.Context, channelID channel.ID, snapshot execution.CredentialSnapshot, _ bool) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		if channelID != channel.Codex {
			return subscriptionruntime.Credential{}, &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Code: "unexpected_subscription_channel"}
		}
		credential, evidence := prepare(ctx, snapshot)
		if evidence != nil {
			return subscriptionruntime.Credential{}, evidence
		}
		converted, err := testRuntimeCredential(service, credential)
		if err != nil {
			return subscriptionruntime.Credential{}, &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Code: "test_credential_invalid", Summary: err.Error()}
		}
		return converted, nil
	}
}

func setCodexModelDiscovery(
	t *testing.T,
	service *Service,
	discover func(context.Context, codex.Credential) ([]codex.Model, error),
) {
	t.Helper()
	service.discoverSubscriptionModels = func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) ([]string, error) {
		if channelID != channel.Codex {
			return nil, errors.New("unexpected subscription channel")
		}
		current, err := testCodexCredential(credential)
		if err != nil {
			return nil, err
		}
		models, err := discover(ctx, current)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids, nil
	}
}

func setCodexAccountObservation(service *Service, observe func(context.Context, codex.Credential) (codex.AccountObservation, error)) {
	hooks := mustCodexControlTestHooks(service)
	hooks.mu.Lock()
	hooks.account = observe
	hooks.mu.Unlock()
}

func setCodexResetCreditObservation(service *Service, observe func(context.Context, codex.Credential) (codex.AccountObservation, error)) {
	hooks := mustCodexControlTestHooks(service)
	hooks.mu.Lock()
	hooks.details = observe
	hooks.mu.Unlock()
}

func setCodexResetCreditConsume(
	t *testing.T,
	service *Service,
	consume func(context.Context, codex.Credential, string) (codex.AccountObservation, error),
) {
	t.Helper()
	service.consumeSubscriptionResetCredit = func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential, requestID string) (subscriptionruntime.ResetCreditResult, error) {
		if channelID != channel.Codex {
			return subscriptionruntime.ResetCreditResult{}, errors.New("unexpected subscription channel")
		}
		current, err := testCodexCredential(credential)
		if err != nil {
			return subscriptionruntime.ResetCreditResult{}, err
		}
		observed, err := consume(ctx, current, requestID)
		if err != nil {
			var upstream *codex.UpstreamHTTPError
			if errors.As(err, &upstream) {
				return subscriptionruntime.ResetCreditResult{}, &subscriptionruntime.UpstreamHTTPError{StatusCode: upstream.StatusCode}
			}
			return subscriptionruntime.ResetCreditResult{}, err
		}
		result, err := normalizeResetCreditConsumeResult(observed.Payload)
		if err != nil {
			return subscriptionruntime.ResetCreditResult{}, err
		}
		return subscriptionruntime.ResetCreditResult{Status: result.Status, WindowsReset: result.WindowsReset, RedeemedAtMS: result.RedeemedAtMS}, nil
	}
}

func mustCodexControlTestHooks(service *Service) *codexControlTestHooks {
	value, ok := codexControlHooks.Load(service)
	if !ok {
		panic("Codex control test hooks were not installed")
	}
	return value.(*codexControlTestHooks)
}

func testRuntimeCredential(service *Service, credential codex.Credential) (subscriptionruntime.Credential, error) {
	canonical, err := codex.MarshalCredential(credential)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	driver, ok := service.subscriptions.Driver(channel.Codex)
	if !ok {
		return subscriptionruntime.Credential{}, errors.New("Codex test driver is unavailable")
	}
	return driver.Parse(canonical)
}

func testCodexCredential(credential subscriptionruntime.Credential) (codex.Credential, error) {
	return codex.ParseCredentialJSON(credential.Canonical())
}

func codexTestVerifier(raw json.RawMessage) string {
	var state struct {
		Verifier string `json:"verifier"`
	}
	_ = json.Unmarshal(raw, &state)
	return state.Verifier
}

// These adapters keep provider fixture payloads in control tests while making
// the production normalization boundary live exclusively in the Codex driver.
func normalizeCodexObservation(raw []byte) (CredentialObservationSnapshot, error) {
	encoded, err := codex.NormalizeQuota(raw, nil)
	if err != nil {
		return CredentialObservationSnapshot{}, err
	}
	var snapshot CredentialObservationSnapshot
	err = json.Unmarshal(encoded, &snapshot)
	return snapshot, err
}

func normalizeCodexResetCreditDetails(raw []byte) (*int64, []ObservationResetCredit, bool, error) {
	encoded, err := codex.NormalizeQuota([]byte(`{}`), raw)
	if err != nil {
		return nil, nil, false, err
	}
	var snapshot CredentialObservationSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, nil, false, err
	}
	return snapshot.ResetCreditsAvailable, snapshot.ResetCredits, snapshot.ResetCredits != nil, nil
}

func normalizeResetCreditConsumeResult(raw []byte) (storedResetCreditResult, error) {
	result, err := codex.NormalizeResetCreditResult(raw)
	if err != nil {
		return storedResetCreditResult{}, err
	}
	return storedResetCreditResult{
		Status: result.Status, WindowsReset: result.WindowsReset, RedeemedAtMS: result.RedeemedAtMS,
	}, nil
}
