package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

const (
	claudeBootstrapBeta      = "oauth-2025-04-20"
	claudeBootstrapUserAgent = "claude-code/2.1.220"
	maxClaudeModels          = 256
	maxClaudeScopedLimits    = 256
	maxClaudeSafeScalarBytes = 512
)

// ErrClaudeAccountObservationUnavailable means no upstream account source
// returned a usable observation. Callers retain the last-known-good snapshot.
var ErrClaudeAccountObservationUnavailable = errors.New("Claude account observation is unavailable")

// ClaudeUpstreamHTTPError retains only the status needed to classify Claude
// companion endpoint failures. Provider response bodies never cross this boundary.
type ClaudeUpstreamHTTPError struct {
	StatusCode int
}

func (err *ClaudeUpstreamHTTPError) Error() string {
	return fmt.Sprintf("Claude OAuth companion endpoint returned status %d", err.StatusCode)
}

// ClaudeModel is the safe model metadata returned by Claude account discovery.
type ClaudeModel struct {
	ID          string
	DisplayName string
	Description string
}

// ClaudeAccountProfile contains only non-secret account presentation fields.
type ClaudeAccountProfile struct {
	DisplayName               string
	Email                     string
	AccountUUID               string
	AccountCreatedAt          string
	OrganizationUUID          string
	OrganizationName          string
	OrganizationType          string
	OrganizationRole          string
	WorkspaceRole             string
	OrganizationRateLimitTier string
	UserRateLimitTier         string
	SeatTier                  string
	BillingType               string
	SubscriptionCreatedAt     string
	ExtraUsageEnabled         *bool
}

// ClaudeUsageWindow is one optional subscription utilization window.
type ClaudeUsageWindow struct {
	Utilization *float64
	ResetsAt    *string
}

// ClaudeExtraUsage describes optional paid usage credits.
type ClaudeExtraUsage struct {
	Enabled        bool
	MonthlyLimit   *float64
	UsedCredits    *float64
	Utilization    *float64
	Currency       *string
	DisabledReason *string
}

// ClaudeScopedLimit is one account, model, or surface-specific quota window.
type ClaudeScopedLimit struct {
	Kind               string
	Group              string
	Percent            float64
	ResetsAt           *string
	ModelDisplayName   string
	SurfaceDisplayName string
}

// ClaudeUsage is the typed Claude OAuth usage response.
type ClaudeUsage struct {
	FiveHour          *ClaudeUsageWindow
	SevenDay          *ClaudeUsageWindow
	SevenDayOAuthApps *ClaudeUsageWindow
	SevenDayOpus      *ClaudeUsageWindow
	SevenDaySonnet    *ClaudeUsageWindow
	CinderCove        *ClaudeUsageWindow
	ExtraUsage        *ClaudeExtraUsage
	Limits            []ClaudeScopedLimit
}

// ClaudeAccountObservation combines account metadata and quota evidence.
type ClaudeAccountObservation struct {
	Profile           ClaudeAccountProfile
	Usage             ClaudeUsage
	Header            http.Header
	IncompleteSources []string
}

type claudeBootstrapResponse struct {
	OrgModelDefault        string `json:"org_model_default"`
	AdditionalModelOptions []struct {
		Model          string  `json:"model"`
		Name           string  `json:"name"`
		Description    string  `json:"description"`
		DisabledReason *string `json:"disabled_reason"`
	} `json:"additional_model_options"`
	ModelAccess []struct {
		APIName  string `json:"api_name"`
		Entitled bool   `json:"entitled"`
	} `json:"model_access"`
	OAuthAccount *struct {
		AccountUUID               string `json:"account_uuid"`
		AccountEmail              string `json:"account_email"`
		OrganizationUUID          string `json:"organization_uuid"`
		OrganizationName          string `json:"organization_name"`
		OrganizationType          string `json:"organization_type"`
		OrganizationRateLimitTier string `json:"organization_rate_limit_tier"`
		UserRateLimitTier         string `json:"user_rate_limit_tier"`
		SeatTier                  string `json:"seat_tier"`
	} `json:"oauth_account"`
}

type claudeOAuthRoles struct {
	OrganizationRole string `json:"organization_role"`
	WorkspaceRole    string `json:"workspace_role"`
	OrganizationName string `json:"organization_name"`
}

type claudeUsageWindowPayload struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeExtraUsagePayload struct {
	Enabled        *bool    `json:"is_enabled"`
	MonthlyLimit   *float64 `json:"monthly_limit"`
	UsedCredits    *float64 `json:"used_credits"`
	Utilization    *float64 `json:"utilization"`
	Currency       *string  `json:"currency"`
	DisabledReason *string  `json:"disabled_reason"`
}

type claudeUsagePayload struct {
	FiveHour          *claudeUsageWindowPayload `json:"five_hour"`
	SevenDay          *claudeUsageWindowPayload `json:"seven_day"`
	SevenDayOAuthApps *claudeUsageWindowPayload `json:"seven_day_oauth_apps"`
	SevenDayOpus      *claudeUsageWindowPayload `json:"seven_day_opus"`
	SevenDaySonnet    *claudeUsageWindowPayload `json:"seven_day_sonnet"`
	CinderCove        *claudeUsageWindowPayload `json:"cinder_cove"`
	ExtraUsage        *claudeExtraUsagePayload  `json:"extra_usage"`
	Limits            []struct {
		Kind     string  `json:"kind"`
		Group    string  `json:"group"`
		Percent  float64 `json:"percent"`
		ResetsAt *string `json:"resets_at"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
			Surface *struct {
				DisplayName string `json:"display_name"`
			} `json:"surface"`
		} `json:"scope"`
	} `json:"limits"`
}

// DiscoverClaudeModels merges CPA's version-pinned Claude catalog with the
// account-specific bootstrap entitlement overlay.
func DiscoverClaudeModels(
	ctx context.Context,
	credential ClaudeCredential,
	options ClaudeOptions,
) ([]ClaudeModel, error) {
	credential, err := canonicalClaudeCredential(credential)
	if err != nil {
		return nil, err
	}
	bootstrap, err := fetchClaudeBootstrap(ctx, credential, options)
	if err != nil {
		return nil, err
	}
	if err := validateClaudeBootstrapIdentity(credential, bootstrap); err != nil {
		return nil, err
	}
	if len(bootstrap.AdditionalModelOptions) > maxClaudeModels ||
		len(bootstrap.ModelAccess) > maxClaudeModels {
		return nil, fmt.Errorf("Claude bootstrap model list is too large")
	}

	denied := make(map[string]struct{})
	for _, option := range bootstrap.AdditionalModelOptions {
		id, idErr := claudeSafeScalar("additional model id", option.Model, true)
		if idErr != nil {
			return nil, idErr
		}
		if option.DisabledReason != nil && strings.TrimSpace(*option.DisabledReason) != "" {
			denied[id] = struct{}{}
		}
	}
	for _, access := range bootstrap.ModelAccess {
		id, idErr := claudeSafeScalar("model access id", access.APIName, true)
		if idErr != nil {
			return nil, idErr
		}
		if !access.Entitled {
			denied[id] = struct{}{}
		}
	}

	models := make(map[string]ClaudeModel)
	for _, model := range registry.GetClaudeModels() {
		if model == nil {
			continue
		}
		id, idErr := claudeSafeScalar("model id", model.ID, true)
		if idErr != nil {
			continue
		}
		displayName, _ := claudeSafeScalar("model display name", model.DisplayName, false)
		description, _ := claudeSafeScalar("model description", model.Description, false)
		if _, blocked := denied[id]; !blocked {
			models[id] = ClaudeModel{ID: id, DisplayName: displayName, Description: description}
		}
	}
	for _, option := range bootstrap.AdditionalModelOptions {
		id, idErr := claudeSafeScalar("additional model id", option.Model, true)
		if idErr != nil {
			return nil, idErr
		}
		if _, blocked := denied[id]; blocked {
			continue
		}
		name, nameErr := claudeSafeScalar("additional model name", option.Name, false)
		if nameErr != nil {
			return nil, nameErr
		}
		description, descriptionErr := claudeSafeScalar("additional model description", option.Description, false)
		if descriptionErr != nil {
			return nil, descriptionErr
		}
		models[id] = ClaudeModel{ID: id, DisplayName: name, Description: description}
	}
	for _, access := range bootstrap.ModelAccess {
		id, idErr := claudeSafeScalar("model access id", access.APIName, true)
		if idErr != nil {
			return nil, idErr
		}
		if !access.Entitled {
			continue
		}
		if _, blocked := denied[id]; blocked {
			continue
		}
		if _, exists := models[id]; !exists {
			models[id] = ClaudeModel{ID: id, DisplayName: id}
		}
	}
	defaultID, err := claudeSafeScalar("organization default model", bootstrap.OrgModelDefault, false)
	if err != nil {
		return nil, err
	}
	if defaultID != "" {
		if _, blocked := denied[defaultID]; !blocked {
			if _, exists := models[defaultID]; !exists {
				models[defaultID] = ClaudeModel{ID: defaultID, DisplayName: defaultID}
			}
		}
	}

	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ClaudeModel, 0, len(ids))
	for _, id := range ids {
		result = append(result, models[id])
	}
	return result, nil
}

// ObserveClaudeAccount fetches the non-mutating Claude Code account endpoints.
func ObserveClaudeAccount(
	ctx context.Context,
	credential ClaudeCredential,
	options ClaudeOptions,
) (ClaudeAccountObservation, error) {
	credential, err := canonicalClaudeCredential(credential)
	if err != nil {
		return ClaudeAccountObservation{}, err
	}
	profile := ClaudeAccountProfile{
		Email: credential.Email, AccountUUID: credential.AccountUUID,
		OrganizationUUID: credential.OrganizationUUID, OrganizationName: credential.OrganizationName,
	}
	incomplete := make([]string, 0, 4)
	successfulSources := 0
	failedSources, commonHTTPStatus := 0, 0
	onlyHTTPFailures := true
	recordFailure := func(sourceErr error) {
		failedSources++
		var upstream *ClaudeUpstreamHTTPError
		if sourceErr == nil || !errors.As(sourceErr, &upstream) {
			onlyHTTPFailures = false
			return
		}
		if commonHTTPStatus == 0 {
			commonHTTPStatus = upstream.StatusCode
		} else if commonHTTPStatus != upstream.StatusCode {
			onlyHTTPFailures = false
		}
	}
	if profilePayload, profileErr := fetchClaudeOAuthProfile(ctx, options, credential.AccessToken); profileErr == nil {
		if err := validateClaudeProfileIdentity(credential, profilePayload); err != nil {
			return ClaudeAccountObservation{}, err
		}
		observedProfile, mapErr := mapClaudeAccountProfile(profilePayload)
		if mapErr != nil {
			incomplete = append(incomplete, "profile")
			recordFailure(nil)
		} else {
			profile = mergeClaudeAccountProfile(profile, observedProfile)
			successfulSources++
		}
	} else if isClaudeObservationContextError(profileErr) {
		return ClaudeAccountObservation{}, profileErr
	} else {
		incomplete = append(incomplete, "profile")
		recordFailure(profileErr)
	}

	if roles, rolesErr := fetchAndDecodeClaudeRoles(ctx, credential, options); rolesErr == nil {
		rolesUsable, rolesComplete := false, true
		if value, valueErr := claudeSafeScalar("organization role", roles.OrganizationRole, false); valueErr == nil {
			if value != "" {
				profile.OrganizationRole = value
				rolesUsable = true
			}
		} else {
			rolesComplete = false
		}
		if value, valueErr := claudeSafeScalar("workspace role", roles.WorkspaceRole, false); valueErr == nil {
			if value != "" {
				profile.WorkspaceRole = value
				rolesUsable = true
			}
		} else {
			rolesComplete = false
		}
		if value, valueErr := claudeSafeScalar("organization name", roles.OrganizationName, false); valueErr == nil && value != "" {
			profile.OrganizationName = value
			rolesUsable = true
		} else if valueErr != nil {
			rolesComplete = false
		}
		if rolesUsable {
			successfulSources++
		}
		if !rolesUsable || !rolesComplete {
			incomplete = append(incomplete, "roles")
		}
		if !rolesUsable {
			recordFailure(nil)
		}
	} else if isClaudeObservationContextError(rolesErr) {
		return ClaudeAccountObservation{}, rolesErr
	} else {
		incomplete = append(incomplete, "roles")
		recordFailure(rolesErr)
	}
	if bootstrap, bootstrapErr := fetchClaudeBootstrap(ctx, credential, options); bootstrapErr == nil {
		if err := validateClaudeBootstrapIdentity(credential, bootstrap); err != nil {
			return ClaudeAccountObservation{}, err
		}
		bootstrapUsable, bootstrapComplete := applyClaudeBootstrapAccount(&profile, bootstrap.OAuthAccount)
		if bootstrapUsable {
			successfulSources++
		}
		if !bootstrapUsable || !bootstrapComplete {
			incomplete = append(incomplete, "bootstrap")
		}
		if !bootstrapUsable {
			recordFailure(nil)
		}
	} else if isClaudeObservationContextError(bootstrapErr) {
		return ClaudeAccountObservation{}, bootstrapErr
	} else {
		incomplete = append(incomplete, "bootstrap")
		recordFailure(bootstrapErr)
	}

	usage, header, err := fetchAndDecodeClaudeUsage(ctx, credential, options)
	if err != nil {
		if isClaudeObservationContextError(err) {
			return ClaudeAccountObservation{}, err
		}
		incomplete = append(incomplete, "usage")
		recordFailure(err)
	} else if claudeUsageUsable(usage) {
		successfulSources++
	} else {
		incomplete = append(incomplete, "usage")
		recordFailure(nil)
	}
	if successfulSources == 0 {
		if failedSources == 4 && onlyHTTPFailures && commonHTTPStatus != 0 {
			return ClaudeAccountObservation{}, &ClaudeUpstreamHTTPError{StatusCode: commonHTTPStatus}
		}
		return ClaudeAccountObservation{}, ErrClaudeAccountObservationUnavailable
	}
	return ClaudeAccountObservation{
		Profile: profile, Usage: usage, Header: header,
		IncompleteSources: incomplete,
	}, nil
}

func isClaudeObservationContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func mergeClaudeAccountProfile(base, observed ClaudeAccountProfile) ClaudeAccountProfile {
	for target, value := range map[*string]string{
		&base.DisplayName: observed.DisplayName, &base.Email: observed.Email,
		&base.AccountUUID: observed.AccountUUID, &base.AccountCreatedAt: observed.AccountCreatedAt,
		&base.OrganizationUUID: observed.OrganizationUUID, &base.OrganizationName: observed.OrganizationName,
		&base.OrganizationType: observed.OrganizationType, &base.OrganizationRole: observed.OrganizationRole,
		&base.WorkspaceRole:             observed.WorkspaceRole,
		&base.OrganizationRateLimitTier: observed.OrganizationRateLimitTier,
		&base.UserRateLimitTier:         observed.UserRateLimitTier, &base.SeatTier: observed.SeatTier,
		&base.BillingType: observed.BillingType, &base.SubscriptionCreatedAt: observed.SubscriptionCreatedAt,
	} {
		if value != "" {
			*target = value
		}
	}
	if observed.ExtraUsageEnabled != nil {
		value := *observed.ExtraUsageEnabled
		base.ExtraUsageEnabled = &value
	}
	return base
}

func canonicalClaudeCredential(credential ClaudeCredential) (ClaudeCredential, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return ClaudeCredential{}, err
	}
	defer clear(raw)
	return ParseClaudeCredentialJSON(raw)
}

func fetchClaudeBootstrap(
	ctx context.Context,
	credential ClaudeCredential,
	options ClaudeOptions,
) (claudeBootstrapResponse, error) {
	endpoint := strings.TrimSpace(options.BootstrapURL)
	if endpoint == "" {
		endpoint = ClaudeBootstrapURL
	}
	body, _, err := fetchClaudeOAuthJSONRequest(
		ctx,
		options,
		endpoint,
		credential.AccessToken,
		func(request *http.Request) {
			request.Header.Set("Anthropic-Beta", claudeBootstrapBeta)
			request.Header.Set("User-Agent", claudeBootstrapUserAgent)
			query := request.URL.Query()
			query.Set("entrypoint", "cli")
			request.URL.RawQuery = query.Encode()
		},
	)
	if err != nil {
		return claudeBootstrapResponse{}, fmt.Errorf("fetch Claude bootstrap: %w", err)
	}
	defer clear(body)
	var bootstrap claudeBootstrapResponse
	if err := decodeClaudeJSONObject(body, &bootstrap); err != nil {
		return claudeBootstrapResponse{}, fmt.Errorf("decode Claude bootstrap: %w", err)
	}
	return bootstrap, nil
}

func fetchAndDecodeClaudeRoles(
	ctx context.Context,
	credential ClaudeCredential,
	options ClaudeOptions,
) (claudeOAuthRoles, error) {
	endpoint := strings.TrimSpace(options.RolesURL)
	if endpoint == "" {
		endpoint = claudeauth.RolesURL
	}
	body, _, err := fetchClaudeOAuthJSONRequest(ctx, options, endpoint, credential.AccessToken, nil)
	if err != nil {
		return claudeOAuthRoles{}, err
	}
	defer clear(body)
	var roles claudeOAuthRoles
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var values []claudeOAuthRoles
		if err := json.Unmarshal(trimmed, &values); err != nil || len(values) > 16 {
			return claudeOAuthRoles{}, fmt.Errorf("invalid Claude roles response")
		}
		if len(values) > 0 {
			roles = values[0]
		}
		return roles, nil
	}
	if err := decodeClaudeJSONObject(trimmed, &roles); err != nil {
		return claudeOAuthRoles{}, err
	}
	return roles, nil
}

func fetchAndDecodeClaudeUsage(
	ctx context.Context,
	credential ClaudeCredential,
	options ClaudeOptions,
) (ClaudeUsage, http.Header, error) {
	endpoint := strings.TrimSpace(options.UsageURL)
	if endpoint == "" {
		endpoint = ClaudeUsageURL
	}
	body, header, err := fetchClaudeOAuthJSONRequest(ctx, options, endpoint, credential.AccessToken, nil)
	if err != nil {
		return ClaudeUsage{}, nil, err
	}
	defer clear(body)
	var payload claudeUsagePayload
	if err := decodeClaudeJSONObject(body, &payload); err != nil {
		return ClaudeUsage{}, nil, err
	}
	usage, err := mapClaudeUsage(payload)
	if err != nil {
		return ClaudeUsage{}, nil, err
	}
	return usage, header, nil
}

func mapClaudeAccountProfile(payload claudeOAuthProfile) (ClaudeAccountProfile, error) {
	values := map[string]string{
		"display name":            payload.Account.DisplayName,
		"email":                   payload.Account.Email,
		"account UUID":            payload.Account.UUID,
		"account created at":      payload.Account.CreatedAt,
		"organization UUID":       payload.Organization.UUID,
		"organization name":       payload.Organization.Name,
		"organization type":       payload.Organization.Type,
		"rate limit tier":         payload.Organization.RateLimitTier,
		"seat tier":               payload.Organization.SeatTier,
		"billing type":            payload.Organization.BillingType,
		"subscription created at": payload.Organization.SubscriptionCreatedAt,
	}
	cleaned := make(map[string]string, len(values))
	for field, value := range values {
		candidate, err := claudeSafeScalar(field, value, field == "account UUID")
		if err != nil {
			return ClaudeAccountProfile{}, err
		}
		cleaned[field] = candidate
	}
	return ClaudeAccountProfile{
		DisplayName: cleaned["display name"], Email: cleaned["email"],
		AccountUUID: cleaned["account UUID"], AccountCreatedAt: cleaned["account created at"],
		OrganizationUUID: cleaned["organization UUID"], OrganizationName: cleaned["organization name"],
		OrganizationType:          cleaned["organization type"],
		OrganizationRateLimitTier: cleaned["rate limit tier"], SeatTier: cleaned["seat tier"],
		BillingType: cleaned["billing type"], SubscriptionCreatedAt: cleaned["subscription created at"],
		ExtraUsageEnabled: payload.Organization.ExtraUsageEnabled,
	}, nil
}

func applyClaudeBootstrapAccount(
	profile *ClaudeAccountProfile,
	account *struct {
		AccountUUID               string `json:"account_uuid"`
		AccountEmail              string `json:"account_email"`
		OrganizationUUID          string `json:"organization_uuid"`
		OrganizationName          string `json:"organization_name"`
		OrganizationType          string `json:"organization_type"`
		OrganizationRateLimitTier string `json:"organization_rate_limit_tier"`
		UserRateLimitTier         string `json:"user_rate_limit_tier"`
		SeatTier                  string `json:"seat_tier"`
	},
) (bool, bool) {
	if profile == nil || account == nil {
		return false, false
	}
	usable, complete := false, true
	for _, value := range []string{account.AccountUUID, account.OrganizationUUID} {
		if cleaned, err := claudeSafeScalar("bootstrap account identity", value, false); err != nil {
			complete = false
		} else if cleaned != "" {
			usable = true
		}
	}
	for target, value := range map[*string]string{
		&profile.Email:                     account.AccountEmail,
		&profile.OrganizationName:          account.OrganizationName,
		&profile.OrganizationType:          account.OrganizationType,
		&profile.OrganizationRateLimitTier: account.OrganizationRateLimitTier,
		&profile.UserRateLimitTier:         account.UserRateLimitTier,
		&profile.SeatTier:                  account.SeatTier,
	} {
		if cleaned, err := claudeSafeScalar("bootstrap account field", value, false); err == nil && cleaned != "" {
			*target = cleaned
			usable = true
		} else if err != nil {
			complete = false
		}
	}
	return usable, complete
}

func claudeUsageUsable(usage ClaudeUsage) bool {
	for _, window := range []*ClaudeUsageWindow{
		usage.FiveHour,
		usage.SevenDay,
		usage.SevenDayOAuthApps,
		usage.SevenDayOpus,
		usage.SevenDaySonnet,
		usage.CinderCove,
	} {
		if window != nil && (window.Utilization != nil ||
			window.ResetsAt != nil && strings.TrimSpace(*window.ResetsAt) != "") {
			return true
		}
	}
	return usage.ExtraUsage != nil || len(usage.Limits) > 0
}

func validateClaudeProfileIdentity(credential ClaudeCredential, profile claudeOAuthProfile) error {
	if strings.TrimSpace(profile.Account.UUID) != credential.AccountUUID {
		return ErrClaudeCredentialIdentityChanged
	}
	if credential.OrganizationUUID != "" && strings.TrimSpace(profile.Organization.UUID) != "" &&
		strings.TrimSpace(profile.Organization.UUID) != credential.OrganizationUUID {
		return ErrClaudeOrganizationIdentityChanged
	}
	return nil
}

func validateClaudeBootstrapIdentity(credential ClaudeCredential, bootstrap claudeBootstrapResponse) error {
	if bootstrap.OAuthAccount == nil {
		return nil
	}
	if value := strings.TrimSpace(bootstrap.OAuthAccount.AccountUUID); value != "" && value != credential.AccountUUID {
		return ErrClaudeCredentialIdentityChanged
	}
	if value := strings.TrimSpace(bootstrap.OAuthAccount.OrganizationUUID); credential.OrganizationUUID != "" &&
		value != "" && value != credential.OrganizationUUID {
		return ErrClaudeOrganizationIdentityChanged
	}
	return nil
}

func mapClaudeUsage(payload claudeUsagePayload) (ClaudeUsage, error) {
	result := ClaudeUsage{}
	var err error
	if result.FiveHour, err = mapClaudeUsageWindow("five_hour", payload.FiveHour); err != nil {
		return ClaudeUsage{}, err
	}
	if result.SevenDay, err = mapClaudeUsageWindow("seven_day", payload.SevenDay); err != nil {
		return ClaudeUsage{}, err
	}
	if result.SevenDayOAuthApps, err = mapClaudeUsageWindow("seven_day_oauth_apps", payload.SevenDayOAuthApps); err != nil {
		return ClaudeUsage{}, err
	}
	if result.SevenDayOpus, err = mapClaudeUsageWindow("seven_day_opus", payload.SevenDayOpus); err != nil {
		return ClaudeUsage{}, err
	}
	if result.SevenDaySonnet, err = mapClaudeUsageWindow("seven_day_sonnet", payload.SevenDaySonnet); err != nil {
		return ClaudeUsage{}, err
	}
	if result.CinderCove, err = mapClaudeUsageWindow("cinder_cove", payload.CinderCove); err != nil {
		return ClaudeUsage{}, err
	}
	if claudeExtraUsagePayloadUsable(payload.ExtraUsage) {
		for field, value := range map[string]*float64{
			"monthly_limit": payload.ExtraUsage.MonthlyLimit,
			"used_credits":  payload.ExtraUsage.UsedCredits,
			"utilization":   payload.ExtraUsage.Utilization,
		} {
			if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
				return ClaudeUsage{}, fmt.Errorf("Claude extra usage %s is invalid", field)
			}
		}
		currency, err := claudeOptionalSafeScalar("currency", payload.ExtraUsage.Currency)
		if err != nil {
			return ClaudeUsage{}, err
		}
		reason, err := claudeOptionalSafeScalar("disabled reason", payload.ExtraUsage.DisabledReason)
		if err != nil {
			return ClaudeUsage{}, err
		}
		result.ExtraUsage = &ClaudeExtraUsage{
			Enabled:      payload.ExtraUsage.Enabled != nil && *payload.ExtraUsage.Enabled,
			MonthlyLimit: cloneFloat64(payload.ExtraUsage.MonthlyLimit),
			UsedCredits:  cloneFloat64(payload.ExtraUsage.UsedCredits),
			Utilization:  cloneFloat64(payload.ExtraUsage.Utilization),
			Currency:     currency, DisabledReason: reason,
		}
	}
	if len(payload.Limits) > maxClaudeScopedLimits {
		return ClaudeUsage{}, fmt.Errorf("Claude scoped limit list is too large")
	}
	result.Limits = make([]ClaudeScopedLimit, 0, len(payload.Limits))
	for _, limit := range payload.Limits {
		if math.IsNaN(limit.Percent) || math.IsInf(limit.Percent, 0) ||
			limit.Percent < 0 || limit.Percent > 100 {
			return ClaudeUsage{}, fmt.Errorf("Claude scoped limit percent is invalid")
		}
		kind, err := claudeSafeScalar("limit kind", limit.Kind, true)
		if err != nil {
			return ClaudeUsage{}, err
		}
		group, err := claudeSafeScalar("limit group", limit.Group, true)
		if err != nil {
			return ClaudeUsage{}, err
		}
		reset, err := claudeOptionalSafeScalar("limit reset", limit.ResetsAt)
		if err != nil {
			return ClaudeUsage{}, err
		}
		item := ClaudeScopedLimit{Kind: kind, Group: group, Percent: limit.Percent, ResetsAt: reset}
		if limit.Scope != nil {
			if limit.Scope.Model != nil {
				item.ModelDisplayName, err = claudeSafeScalar(
					"limit model display name", limit.Scope.Model.DisplayName, false,
				)
				if err != nil {
					return ClaudeUsage{}, err
				}
			}
			if limit.Scope.Surface != nil {
				item.SurfaceDisplayName, err = claudeSafeScalar(
					"limit surface display name", limit.Scope.Surface.DisplayName, false,
				)
				if err != nil {
					return ClaudeUsage{}, err
				}
			}
		}
		result.Limits = append(result.Limits, item)
	}
	return result, nil
}

func claudeExtraUsagePayloadUsable(payload *claudeExtraUsagePayload) bool {
	return payload != nil && (payload.Enabled != nil || payload.MonthlyLimit != nil ||
		payload.UsedCredits != nil || payload.Utilization != nil || payload.Currency != nil ||
		payload.DisabledReason != nil)
}

func mapClaudeUsageWindow(name string, payload *claudeUsageWindowPayload) (*ClaudeUsageWindow, error) {
	if payload == nil {
		return nil, nil
	}
	if payload.Utilization != nil &&
		(math.IsNaN(*payload.Utilization) || math.IsInf(*payload.Utilization, 0) ||
			*payload.Utilization < 0 || *payload.Utilization > 100) {
		return nil, fmt.Errorf("Claude %s utilization is invalid", name)
	}
	reset, err := claudeOptionalSafeScalar(name+" reset", payload.ResetsAt)
	if err != nil {
		return nil, err
	}
	return &ClaudeUsageWindow{Utilization: cloneFloat64(payload.Utilization), ResetsAt: reset}, nil
}

func fetchClaudeOAuthJSONRequest(
	ctx context.Context,
	options ClaudeOptions,
	endpoint string,
	accessToken string,
	configure func(*http.Request),
) ([]byte, http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, nil, fmt.Errorf("Claude companion endpoint is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	applyClaudeOAuthHeaders(request)
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Cache-Control", "no-cache")
	if configure != nil {
		configure(request)
	}
	response, err := claudeOAuthClient(options).Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := readClaudeOAuthBody(response)
	if err != nil {
		return nil, nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		clear(body)
		return nil, nil, &ClaudeUpstreamHTTPError{StatusCode: response.StatusCode}
	}
	return body, response.Header.Clone(), nil
}

func decodeClaudeJSONObject(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return fmt.Errorf("response must be one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("response contains multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("response has invalid trailing content")
	}
	return nil
}

func claudeSafeScalar(field, value string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("Claude %s is required", field)
	}
	if len(value) > maxClaudeSafeScalarBytes || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("Claude %s is invalid", field)
	}
	return value, nil
}

func claudeOptionalSafeScalar(field string, value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	cleaned, err := claudeSafeScalar(field, *value, false)
	if err != nil {
		return nil, err
	}
	return &cleaned, nil
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
