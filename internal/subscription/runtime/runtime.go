package subscriptionruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/modules"
)

// Credential is a provider-neutral, immutable subscription credential. Only
// the bound driver knows how to decode its canonical representation.
type Credential struct {
	canonical    []byte
	identity     string
	account      Account
	expiresAt    time.Time
	expires      bool
	secretValues []string
}

// Account contains the safe account information shared by subscription UIs.
type Account struct {
	Email            string
	ExpiresAt        time.Time
	ExpiresAtKnown   bool
	LastRefresh      time.Time
	LastRefreshKnown bool
}

func newCredential(canonical []byte, identity string, account Account, expiresAt time.Time, expires bool, secrets []string) Credential {
	return Credential{
		canonical: append([]byte(nil), canonical...), identity: identity, account: account,
		expiresAt: expiresAt, expires: expires, secretValues: append([]string(nil), secrets...),
	}
}

// Canonical returns an independent copy suitable for encryption or adapter decoding.
func (credential Credential) Canonical() []byte { return append([]byte(nil), credential.canonical...) }

// Identity returns the stable provider account identity used across refreshes.
func (credential Credential) Identity() string { return credential.identity }

// Account returns safe display metadata.
func (credential Credential) Account() Account { return credential.account }

// ExpiresAt returns the best known access-token expiry.
func (credential Credential) ExpiresAt() (time.Time, bool) {
	return credential.expiresAt, credential.expires
}

// SecretValues returns independent sensitive values used only for redaction.
func (credential Credential) SecretValues() []string {
	return append([]string(nil), credential.secretValues...)
}

// RefreshFailure classifies lifecycle failures without leaking provider error types.
type RefreshFailure uint8

const (
	RefreshFailureOutcomeUnknown RefreshFailure = iota
	RefreshFailureReauthorizationRequired
	RefreshFailureIdentityChanged
)

// Authorization describes one short-lived browser authorization challenge.
type Authorization struct {
	URL         string
	State       string
	DriverState []byte
	ExpiresAt   time.Time
	// LocalCallback asks the control plane to start its restricted localhost
	// callback listener for this authorization flow.
	LocalCallback bool
}

// AuthorizationCompletion is the provider-neutral callback input.
type AuthorizationCompletion struct {
	ExpectedState string
	ReturnedState string
	Code          string
	DriverState   []byte
}

// Driver owns one subscription credential schema and refresh lifecycle.
type Driver interface {
	ID() modules.SubscriptionDriverID
	Parse([]byte) (Credential, error)
	Refresh(context.Context, Credential) (Credential, error)
	ClassifyRefreshFailure(error) RefreshFailure
}

// BrowserAuthorizationDriver is implemented only by subscription channels
// which support interactive browser authorization.
type BrowserAuthorizationDriver interface {
	Driver
	BeginAuthorization() (Authorization, error)
	CompleteAuthorization(context.Context, AuthorizationCompletion) (Credential, error)
	AuthorizationFailureDefinitive(error) bool
	RequiresLocalCallback() bool
}

// ModelDiscovery is a narrow optional subscription capability.
type ModelDiscovery interface {
	ID() modules.UtilityID
	DiscoverModels(context.Context, Credential) ([]string, error)
}

// ErrObservationPayloadInvalid distinguishes an upstream payload that the
// channel capability could not normalize from an upstream request failure.
var ErrObservationPayloadInvalid = errors.New("subscription observation payload invalid")

// Observation contains the canonical, provider-neutral observation JSON.
// Headers are retained only for bounded metadata such as retry timing.
type Observation struct {
	Payload []byte
	Header  http.Header
}

// QuotaObservation is a narrow optional account observation capability.
type QuotaObservation interface {
	ID() modules.UtilityID
	Observe(context.Context, Credential) (Observation, error)
}

// ResetCreditAction is the currently supported mutating subscription action.
type ResetCreditAction interface {
	ID() modules.ActionID
	Consume(context.Context, Credential, string) (ResetCreditResult, error)
}

// ResetCreditResult is the provider-neutral result of one durable credit action.
type ResetCreditResult struct {
	Status       string
	WindowsReset int
	RedeemedAtMS *int64
}

// UpstreamHTTPError is a provider-neutral status classification. Bodies and
// provider credentials never cross this boundary.
type UpstreamHTTPError struct {
	StatusCode int
}

func (err *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("subscription upstream returned status %d", err.StatusCode)
}

type channelRuntime struct {
	driver      Driver
	discovery   ModelDiscovery
	observation QuotaObservation
	resetCredit ResetCreditAction
}

// Runtime is the immutable, startup-compiled subscription capability registry.
// Request paths resolve ChannelID directly to already-bound implementations.
type Runtime struct {
	byChannel map[channel.ID]channelRuntime
}

// NewRuntime compiles all built-in drivers and capabilities against the same
// channel registry used by state, scheduling and execution.
func NewRuntime(channels *channel.Registry) (*Runtime, error) {
	codex := newCodexDriver()
	return compileRuntime(
		channels,
		[]Driver{codex},
		[]ModelDiscovery{codex.modelDiscovery()},
		[]QuotaObservation{codex.quotaObservation()},
		[]ResetCreditAction{codex.resetCreditAction()},
	)
}

func compileRuntime(
	channels *channel.Registry,
	drivers []Driver,
	discoveries []ModelDiscovery,
	observations []QuotaObservation,
	resetCredits []ResetCreditAction,
) (*Runtime, error) {
	if channels == nil {
		return nil, errors.New("compile subscription runtime: channel registry is unavailable")
	}
	driverByID, err := indexImplementations(drivers, func(value Driver) modules.ExtensionID {
		if value == nil {
			return ""
		}
		return modules.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription drivers: %w", err)
	}
	discoveryByID, err := indexImplementations(discoveries, func(value ModelDiscovery) modules.ExtensionID {
		if value == nil {
			return ""
		}
		return modules.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription model discovery: %w", err)
	}
	observationByID, err := indexImplementations(observations, func(value QuotaObservation) modules.ExtensionID {
		if value == nil {
			return ""
		}
		return modules.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription observation: %w", err)
	}
	resetByID, err := indexImplementations(resetCredits, func(value ResetCreditAction) modules.ExtensionID {
		if value == nil {
			return ""
		}
		return modules.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription actions: %w", err)
	}

	result := &Runtime{byChannel: make(map[channel.ID]channelRuntime)}
	for _, descriptor := range channels.List() {
		bindings, ok := channels.CapabilityBindings(descriptor.ID)
		if !ok {
			return nil, fmt.Errorf("compile subscription runtime: channel %q disappeared", descriptor.ID)
		}
		if descriptor.Connection.Type != string(modules.ConnectionSubscription) {
			if bindings.SubscriptionDriver != "" || bindings.ModelDiscovery != "" ||
				bindings.QuotaObservation != "" || len(bindings.Actions) != 0 {
				return nil, fmt.Errorf("compile subscription runtime: API key channel %q binds subscription capabilities", descriptor.ID)
			}
			continue
		}
		driver, ok := driverByID[modules.ExtensionID(bindings.SubscriptionDriver)]
		if !ok {
			return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown driver %q", descriptor.ID, bindings.SubscriptionDriver)
		}
		compiled := channelRuntime{driver: driver}
		if bindings.ModelDiscovery != "" {
			compiled.discovery, ok = discoveryByID[modules.ExtensionID(bindings.ModelDiscovery)]
			if !ok {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown model discovery %q", descriptor.ID, bindings.ModelDiscovery)
			}
		}
		if bindings.QuotaObservation != "" {
			compiled.observation, ok = observationByID[modules.ExtensionID(bindings.QuotaObservation)]
			if !ok {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown quota observation %q", descriptor.ID, bindings.QuotaObservation)
			}
		}
		for _, actionID := range bindings.Actions {
			action, exists := resetByID[modules.ExtensionID(actionID)]
			if !exists {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown action %q", descriptor.ID, actionID)
			}
			if compiled.resetCredit != nil {
				return nil, fmt.Errorf("compile subscription runtime: channel %q binds multiple reset-credit actions", descriptor.ID)
			}
			compiled.resetCredit = action
		}
		result.byChannel[descriptor.ID] = compiled
	}
	return result, nil
}

func indexImplementations[T any](values []T, id func(T) modules.ExtensionID) (map[modules.ExtensionID]T, error) {
	result := make(map[modules.ExtensionID]T, len(values))
	for _, value := range values {
		key := id(value)
		if key == "" {
			return nil, errors.New("implementation has an empty ID")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate implementation %q", key)
		}
		result[key] = value
	}
	return result, nil
}

// Driver resolves the lifecycle implementation already bound to a channel.
func (runtime *Runtime) Driver(channelID channel.ID) (Driver, bool) {
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.driver, ok && value.driver != nil
}

// BrowserAuthorization resolves the optional interactive authorization driver.
func (runtime *Runtime) BrowserAuthorization(channelID channel.ID) (BrowserAuthorizationDriver, bool) {
	driver, ok := runtime.Driver(channelID)
	if !ok {
		return nil, false
	}
	browser, ok := driver.(BrowserAuthorizationDriver)
	return browser, ok
}

// CanonicalCredential validates one channel-bound subscription credential and
// returns an independent canonical representation. It is intentionally small
// so startup loaders can depend on the behavior without importing this package.
func (runtime *Runtime) CanonicalCredential(channelID channel.ID, raw []byte) ([]byte, error) {
	driver, ok := runtime.Driver(channelID)
	if !ok {
		return nil, fmt.Errorf("subscription driver for channel %q is unavailable", channelID)
	}
	credential, err := driver.Parse(raw)
	if err != nil {
		return nil, err
	}
	return credential.Canonical(), nil
}

func (runtime *Runtime) ModelDiscovery(channelID channel.ID) (ModelDiscovery, bool) {
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.discovery, ok && value.discovery != nil
}

func (runtime *Runtime) QuotaObservation(channelID channel.ID) (QuotaObservation, bool) {
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.observation, ok && value.observation != nil
}

func (runtime *Runtime) ResetCreditAction(channelID channel.ID) (ResetCreditAction, bool) {
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.resetCredit, ok && value.resetCredit != nil
}

// ChannelIDs returns the stable set with a compiled subscription driver.
func (runtime *Runtime) ChannelIDs() []channel.ID {
	if runtime == nil {
		return nil
	}
	result := make([]channel.ID, 0, len(runtime.byChannel))
	for id := range runtime.byChannel {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// QuotaObservationChannelIDs returns channels with the compiled observation capability.
func (runtime *Runtime) QuotaObservationChannelIDs() []channel.ID {
	if runtime == nil {
		return nil
	}
	result := make([]channel.ID, 0, len(runtime.byChannel))
	for id, compiled := range runtime.byChannel {
		if compiled.observation != nil {
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
