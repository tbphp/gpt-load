package subscriptionruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/channel/spec"
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

func NewCredential(canonical []byte, identity string, account Account, expiresAt time.Time, expires bool, secrets []string) Credential {
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
	RefreshFailureRetryable
	RefreshFailureReauthorizationRequired
	RefreshFailureIdentityChanged
)

// RefreshFailureDecision contains only bounded, provider-neutral diagnostics.
// Raw errors and token endpoint response bodies must not cross this boundary.
type RefreshFailureDecision struct {
	Kind       RefreshFailure
	StatusCode int
	OAuthCode  string
	RetryAfter time.Duration
}

func (failure RefreshFailure) String() string {
	switch failure {
	case RefreshFailureRetryable:
		return "retryable"
	case RefreshFailureReauthorizationRequired:
		return "reauthorization_required"
	case RefreshFailureIdentityChanged:
		return "identity_changed"
	default:
		return "outcome_unknown"
	}
}

// Authorization describes one short-lived browser authorization challenge.
type Authorization struct {
	URL         string
	State       string
	DriverState []byte
	ExpiresAt   time.Time
	// RedirectURI is populated by the compiled runtime from the driver's fixed
	// local callback declaration. It is safe to expose with the Stage result.
	RedirectURI string
}

// LocalCallbackSpec declares one fixed loopback OAuth redirect endpoint.
type LocalCallbackSpec struct {
	RedirectURI string
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
	ID() spec.SubscriptionDriverID
	Parse([]byte) (Credential, error)
	Refresh(context.Context, Credential) (Credential, error)
	ClassifyRefreshFailure(error) RefreshFailureDecision
}

// CredentialFileImporter is an optional, narrow preprocessing capability for
// OAuth file formats that cannot safely provide a canonical credential without
// one provider-bound verification step. Existing drivers continue to use Parse.
type CredentialFileImporter interface {
	ImportCredential(context.Context, []byte) (Credential, error)
}

// BrowserAuthorizationDriver is implemented only by subscription channels
// which support interactive browser authorization.
type BrowserAuthorizationDriver interface {
	Driver
	BeginAuthorization() (Authorization, error)
	CompleteAuthorization(context.Context, AuthorizationCompletion) (Credential, error)
	AuthorizationFailureDefinitive(error) bool
	LocalCallback() (LocalCallbackSpec, bool)
}

// DeviceAuthorization describes one RFC 8628 device challenge. DriverState
// contains secrets and is persisted only inside the encrypted Stage payload.
type DeviceAuthorization struct {
	VerificationURL string
	UserCode        string
	DriverState     []byte
	ExpiresAt       time.Time
	PollInterval    time.Duration
}

// DeviceAuthorizationStatus is the bounded result of one token-endpoint poll.
type DeviceAuthorizationStatus string

const (
	DeviceAuthorizationPending    DeviceAuthorizationStatus = "pending"
	DeviceAuthorizationAuthorized DeviceAuthorizationStatus = "authorized"
	DeviceAuthorizationDenied     DeviceAuthorizationStatus = "denied"
	DeviceAuthorizationExpired    DeviceAuthorizationStatus = "expired"
)

// DeviceAuthorizationPoll contains the result of exactly one token request.
// Pending results may replace DriverState and adjust PollInterval (slow_down).
type DeviceAuthorizationPoll struct {
	Status       DeviceAuthorizationStatus
	Credential   Credential
	DriverState  []byte
	PollInterval time.Duration
}

// DeviceAuthorizationDriver is implemented by subscription channels whose
// interactive flow uses a device code rather than an OAuth redirect callback.
type DeviceAuthorizationDriver interface {
	Driver
	BeginDeviceAuthorization(context.Context) (DeviceAuthorization, error)
	PollDeviceAuthorization(context.Context, []byte) (DeviceAuthorizationPoll, error)
}

// ModelDiscovery is a narrow optional subscription capability.
type ModelDiscovery interface {
	ID() spec.UtilityID
	DiscoverModels(context.Context, Credential) ([]string, error)
}

// ErrObservationPayloadInvalid distinguishes an upstream payload that the
// channel capability could not normalize from an upstream request failure.
var ErrObservationPayloadInvalid = errors.New("subscription observation payload invalid")

// Observation contains the canonical, provider-neutral observation JSON.
// Headers are retained only for bounded metadata such as retry timing.
type Observation struct {
	Payload         []byte
	Header          http.Header
	Partial         bool
	AccountObserved bool
	QuotaObserved   bool
	// ObservedQuotaScopes lists quota scopes whose current payload is authoritative.
	// Unlisted scopes may retain a still-fresh previous observation.
	ObservedQuotaScopes []string
}

// QuotaObservation is a narrow optional account observation capability.
type QuotaObservation interface {
	ID() spec.UtilityID
	Observe(context.Context, Credential) (Observation, error)
}

// ResetCreditAction is the currently supported mutating subscription action.
type ResetCreditAction interface {
	ID() spec.ActionID
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
	browser     BrowserAuthorizationDriver
	device      DeviceAuthorizationDriver
	callback    *LocalCallbackSpec
	discovery   ModelDiscovery
	observation QuotaObservation
	resetCredit ResetCreditAction
}

// Runtime is the immutable, startup-compiled subscription capability registry.
// Request paths resolve ChannelID directly to already-bound implementations.
type Runtime struct {
	byChannel map[channel.ID]channelRuntime
}

// Implementations is the explicit composition-root input for subscription behavior.
type Implementations struct {
	Drivers            []Driver
	ModelDiscoveries   []ModelDiscovery
	QuotaObservations  []QuotaObservation
	ResetCreditActions []ResetCreditAction
}

// NewRuntime compiles explicitly supplied drivers and capabilities against the
// same channel registry used by state, scheduling and execution.
func NewRuntime(channels *channel.Registry, registrations ...Implementations) (*Runtime, error) {
	var implementations Implementations
	for _, registration := range registrations {
		implementations.Drivers = append(implementations.Drivers, registration.Drivers...)
		implementations.ModelDiscoveries = append(implementations.ModelDiscoveries, registration.ModelDiscoveries...)
		implementations.QuotaObservations = append(implementations.QuotaObservations, registration.QuotaObservations...)
		implementations.ResetCreditActions = append(implementations.ResetCreditActions, registration.ResetCreditActions...)
	}
	return compileRuntime(
		channels,
		implementations.Drivers,
		implementations.ModelDiscoveries,
		implementations.QuotaObservations,
		implementations.ResetCreditActions,
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
	driverByID, err := indexImplementations(drivers, func(value Driver) spec.ExtensionID {
		if value == nil {
			return ""
		}
		return spec.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription drivers: %w", err)
	}
	discoveryByID, err := indexImplementations(discoveries, func(value ModelDiscovery) spec.ExtensionID {
		if value == nil {
			return ""
		}
		return spec.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription model discovery: %w", err)
	}
	observationByID, err := indexImplementations(observations, func(value QuotaObservation) spec.ExtensionID {
		if value == nil {
			return ""
		}
		return spec.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription observation: %w", err)
	}
	resetByID, err := indexImplementations(resetCredits, func(value ResetCreditAction) spec.ExtensionID {
		if value == nil {
			return ""
		}
		return spec.ExtensionID(value.ID())
	})
	if err != nil {
		return nil, fmt.Errorf("compile subscription actions: %w", err)
	}

	result := &Runtime{byChannel: make(map[channel.ID]channelRuntime)}
	callbackChannels := make(map[string]channel.ID)
	for _, descriptor := range channels.List() {
		bindings, ok := channels.CapabilityBindings(descriptor.ID)
		if !ok {
			return nil, fmt.Errorf("compile subscription runtime: channel %q disappeared", descriptor.ID)
		}
		if descriptor.Connection.Type != string(spec.ConnectionSubscription) {
			if bindings.SubscriptionDriver != "" || bindings.ModelDiscovery != "" ||
				bindings.QuotaObservation != "" || bindings.ResetCreditAction != "" {
				return nil, fmt.Errorf("compile subscription runtime: API key channel %q binds subscription capabilities", descriptor.ID)
			}
			continue
		}
		driver, ok := driverByID[spec.ExtensionID(bindings.SubscriptionDriver)]
		if !ok {
			return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown driver %q", descriptor.ID, bindings.SubscriptionDriver)
		}
		compiled := channelRuntime{driver: driver}
		interactiveMethods := 0
		for _, method := range descriptor.Connection.AuthorizationMethods {
			switch method {
			case channel.AuthorizationBrowserOAuth:
				interactiveMethods++
				compiled.browser, ok = driver.(BrowserAuthorizationDriver)
				if !ok {
					return nil, fmt.Errorf("compile subscription runtime: channel %q declares browser OAuth without driver support", descriptor.ID)
				}
				callback, local := compiled.browser.LocalCallback()
				if local {
					callback, err = validateLocalCallbackSpec(callback)
					if err != nil {
						return nil, fmt.Errorf("compile subscription runtime: channel %q has invalid local callback: %w", descriptor.ID, err)
					}
					if owner, duplicate := callbackChannels[callback.RedirectURI]; duplicate {
						return nil, fmt.Errorf("compile subscription runtime: channels %q and %q share local callback %q", owner, descriptor.ID, callback.RedirectURI)
					}
					callbackChannels[callback.RedirectURI] = descriptor.ID
					compiled.callback = &callback
				}
			case channel.AuthorizationDeviceOAuth:
				interactiveMethods++
				compiled.device, ok = driver.(DeviceAuthorizationDriver)
				if !ok {
					return nil, fmt.Errorf("compile subscription runtime: channel %q declares device OAuth without driver support", descriptor.ID)
				}
			}
		}
		if interactiveMethods > 1 {
			return nil, fmt.Errorf("compile subscription runtime: channel %q declares multiple interactive authorization methods", descriptor.ID)
		}
		if bindings.ModelDiscovery != "" {
			compiled.discovery, ok = discoveryByID[spec.ExtensionID(bindings.ModelDiscovery)]
			if !ok {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown model discovery %q", descriptor.ID, bindings.ModelDiscovery)
			}
		}
		if bindings.QuotaObservation != "" {
			compiled.observation, ok = observationByID[spec.ExtensionID(bindings.QuotaObservation)]
			if !ok {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown quota observation %q", descriptor.ID, bindings.QuotaObservation)
			}
		}
		if bindings.ResetCreditAction != "" {
			compiled.resetCredit, ok = resetByID[spec.ExtensionID(bindings.ResetCreditAction)]
			if !ok {
				return nil, fmt.Errorf("compile subscription runtime: channel %q references unknown action %q", descriptor.ID, bindings.ResetCreditAction)
			}
		}
		result.byChannel[descriptor.ID] = compiled
	}
	return result, nil
}

func validateLocalCallbackSpec(spec LocalCallbackSpec) (LocalCallbackSpec, error) {
	raw := strings.TrimSpace(spec.RedirectURI)
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || !strings.EqualFold(parsed.Scheme, "http") ||
		!strings.EqualFold(parsed.Hostname(), "localhost") || parsed.Port() == "" ||
		parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") || parsed.EscapedPath() != parsed.Path {
		return LocalCallbackSpec{}, errors.New("redirect URI must be a fixed http://localhost endpoint")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return LocalCallbackSpec{}, errors.New("redirect URI has an invalid port")
	}
	canonical := "http://localhost:" + strconv.Itoa(port) + parsed.Path
	if raw != canonical {
		return LocalCallbackSpec{}, errors.New("redirect URI is not canonical")
	}
	return LocalCallbackSpec{RedirectURI: canonical}, nil
}

func indexImplementations[T any](values []T, id func(T) spec.ExtensionID) (map[spec.ExtensionID]T, error) {
	result := make(map[spec.ExtensionID]T, len(values))
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
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.browser, ok && value.browser != nil
}

// DeviceAuthorization resolves the optional device authorization driver.
func (runtime *Runtime) DeviceAuthorization(channelID channel.ID) (DeviceAuthorizationDriver, bool) {
	if runtime == nil {
		return nil, false
	}
	value, ok := runtime.byChannel[channelID]
	return value.device, ok && value.device != nil
}

// LocalCallback returns the startup-validated fixed callback for a channel.
func (runtime *Runtime) LocalCallback(channelID channel.ID) (LocalCallbackSpec, bool) {
	if runtime == nil {
		return LocalCallbackSpec{}, false
	}
	value, ok := runtime.byChannel[channelID]
	if !ok || value.callback == nil {
		return LocalCallbackSpec{}, false
	}
	return *value.callback, true
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

// ImportCredential prepares one OAuth-file credential for a ready stage. Most
// channels use Parse directly; a provider that lacks a stable identity in its
// native file may implement CredentialFileImporter for one bounded enrichment
// step before any persistence or duplicate check occurs.
func (runtime *Runtime) ImportCredential(
	ctx context.Context,
	channelID channel.ID,
	raw []byte,
) (Credential, error) {
	driver, ok := runtime.Driver(channelID)
	if !ok {
		return Credential{}, fmt.Errorf("subscription driver for channel %q is unavailable", channelID)
	}
	if importer, ok := driver.(CredentialFileImporter); ok {
		return importer.ImportCredential(ctx, raw)
	}
	return driver.Parse(raw)
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
