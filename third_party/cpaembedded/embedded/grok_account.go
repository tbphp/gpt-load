package embedded

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	grokBillingWeeklyURL  = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	grokBillingMonthlyURL = "https://cli-chat-proxy.grok.com/v1/billing"
	maxGrokBillingBytes   = 256 << 10
	maxGrokProductUsage   = 128
	maxGrokSafeNumber     = 1<<53 - 1
)

var (
	ErrGrokAccountObservationUnavailable    = errors.New("Grok account observation is unavailable")
	ErrGrokAccountObservationPayloadInvalid = errors.New("Grok account observation payload is invalid")
)

type GrokProductUsage struct {
	Product      string
	UsagePercent *float64
}

type GrokBillingObservation struct {
	PeriodType         string
	PeriodStart        string
	PeriodEnd          string
	UsagePercent       *float64
	ProductUsage       []GrokProductUsage
	MonthlyLimitCents  *float64
	UsedCents          *float64
	OnDemandCapCents   *float64
	OnDemandUsedCents  *float64
	BillingPeriodStart string
	BillingPeriodEnd   string
}

type GrokAccountObservation struct {
	Billing              GrokBillingObservation
	Tier                 *int
	Header               http.Header
	AccountObserved      bool
	AccountQuotaObserved bool
	SurfaceQuotaObserved bool
	CreditQuotaObserved  bool
	IncompleteSources    []string
}

type grokBillingPresence struct {
	account            bool
	period             bool
	usage              bool
	surface            bool
	products           bool
	credits            bool
	monthlyLimit       bool
	used               bool
	onDemandCap        bool
	onDemandUsed       bool
	billingPeriodStart bool
	billingPeriodEnd   bool
}

type grokBillingResult struct {
	billing  GrokBillingObservation
	presence grokBillingPresence
	header   http.Header
	err      error
}

func ObserveGrokAccount(
	ctx context.Context,
	credential GrokCredential,
	options GrokOptions,
) (GrokAccountObservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizeGrokCredential(&credential)
	if err := validateGrokCredentialWithOptions(credential, options); err != nil {
		return GrokAccountObservation{}, err
	}
	weeklyURL := strings.TrimSpace(options.BillingWeeklyURL)
	if weeklyURL == "" {
		weeklyURL = grokBillingWeeklyURL
	}
	monthlyURL := strings.TrimSpace(options.BillingMonthlyURL)
	if monthlyURL == "" {
		monthlyURL = grokBillingMonthlyURL
	}
	sources := []struct {
		name string
		url  string
	}{
		{name: "weekly", url: weeklyURL},
		{name: "monthly", url: monthlyURL},
	}
	results := make([]grokBillingResult, len(sources))
	var wait sync.WaitGroup
	for index, source := range sources {
		wait.Add(1)
		go func(index int, sourceURL string) {
			defer wait.Done()
			results[index] = fetchGrokBilling(ctx, credential, sourceURL, options)
		}(index, source.url)
	}
	wait.Wait()
	if err := context.Cause(ctx); err != nil {
		return GrokAccountObservation{}, err
	}

	observation := GrokAccountObservation{Tier: grokCredentialTier(credential)}
	var observedPresence grokBillingPresence
	usableSources := 0
	for index, result := range results {
		if result.err != nil {
			observation.IncompleteSources = append(observation.IncompleteSources, sources[index].name)
			continue
		}
		usableSources++
		observation.Billing, observedPresence = mergeGrokBilling(
			observation.Billing,
			observedPresence,
			result.billing,
			result.presence,
		)
		if observation.Header == nil {
			observation.Header = result.header.Clone()
		}
	}
	if usableSources == 0 {
		return GrokAccountObservation{}, classifyGrokObservationFailure(results)
	}
	observation.AccountQuotaObserved = observedPresence.account
	observation.SurfaceQuotaObserved = observedPresence.surface
	observation.CreditQuotaObserved = observedPresence.credits
	observation.AccountObserved = observation.Tier != nil || observation.CreditQuotaObserved
	sort.Strings(observation.IncompleteSources)
	return observation, nil
}

func fetchGrokBilling(
	ctx context.Context,
	credential GrokCredential,
	endpoint string,
	options GrokOptions,
) grokBillingResult {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return grokBillingResult{err: err}
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	request.Header.Set("x-grok-client-version", grokClientVersion)
	request.Header.Set("User-Agent", "grok-pager/"+grokClientVersion+" grok-shell/"+grokClientVersion)
	request.Header.Set("x-grok-client-identifier", "grok-shell")
	request.Header.Set("x-authenticateresponse", "authenticate-response")
	request.Header.Set("x-userid", credential.AccountID)
	response, err := grokHTTPClient(options).Do(request)
	if err != nil {
		return grokBillingResult{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGrokBillingBytes+1))
	if err != nil {
		return grokBillingResult{err: err}
	}
	defer clear(body)
	if len(body) > maxGrokBillingBytes {
		return grokBillingResult{err: ErrGrokAccountObservationPayloadInvalid}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return grokBillingResult{err: &GrokUpstreamHTTPError{Operation: "billing", StatusCode: response.StatusCode}}
	}
	billing, presence, err := decodeGrokBilling(body, grokNow(options))
	if err != nil {
		return grokBillingResult{err: err}
	}
	return grokBillingResult{billing: billing, presence: presence, header: response.Header.Clone()}
}

func decodeGrokBilling(body []byte, now time.Time) (GrokBillingObservation, grokBillingPresence, error) {
	root, err := decodeGrokJSONObject(body)
	if err != nil {
		return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
	}
	configRaw, ok := root["config"]
	if !ok || bytes.Equal(bytes.TrimSpace(configRaw), []byte("null")) {
		return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
	}
	config, err := decodeGrokJSONObject(configRaw)
	if err != nil {
		return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
	}
	var billing GrokBillingObservation
	var presence grokBillingPresence
	unifiedBilling := false
	if raw, exists := grokRawField(config, "currentPeriod", "current_period"); exists {
		period, usable, periodErr := decodeGrokPeriod(raw)
		if periodErr != nil {
			return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
		}
		if usable {
			billing.PeriodType, billing.PeriodStart, billing.PeriodEnd = period[0], period[1], period[2]
			presence.account = true
			presence.period = true
		}
	}
	if raw, exists := grokRawField(config, "creditUsagePercent", "credit_usage_percent"); exists {
		value, valueErr := grokOptionalNumber(raw, 100)
		if valueErr != nil {
			return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
		}
		if value != nil {
			billing.UsagePercent = value
			presence.account = true
			presence.usage = true
		}
	}
	if raw, exists := grokRawField(config, "productUsage", "product_usage"); exists {
		values, valueErr := decodeGrokProductUsage(raw)
		if valueErr != nil {
			return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
		}
		billing.ProductUsage = values
		presence.surface = true
		presence.products = true
	}
	if raw, exists := grokRawField(config, "isUnifiedBillingUser", "is_unified_billing_user"); exists &&
		!bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &unifiedBilling); err != nil {
			return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
		}
	}
	for _, field := range []struct {
		names    []string
		target   **float64
		presence *bool
	}{
		{names: []string{"monthlyLimit", "monthly_limit"}, target: &billing.MonthlyLimitCents, presence: &presence.monthlyLimit},
		{names: []string{"used"}, target: &billing.UsedCents, presence: &presence.used},
		{names: []string{"onDemandCap", "on_demand_cap"}, target: &billing.OnDemandCapCents, presence: &presence.onDemandCap},
		{names: []string{"onDemandUsed", "on_demand_used"}, target: &billing.OnDemandUsedCents, presence: &presence.onDemandUsed},
	} {
		if raw, exists := grokRawField(config, field.names...); exists {
			value, valueErr := grokOptionalCentValue(raw)
			if valueErr != nil {
				return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
			}
			*field.target = value
			*field.presence = true
			presence.credits = true
		}
	}
	for _, field := range []struct {
		names    []string
		target   *string
		presence *bool
	}{
		{names: []string{"billingPeriodStart", "billing_period_start"}, target: &billing.BillingPeriodStart, presence: &presence.billingPeriodStart},
		{names: []string{"billingPeriodEnd", "billing_period_end"}, target: &billing.BillingPeriodEnd, presence: &presence.billingPeriodEnd},
	} {
		if raw, exists := grokRawField(config, field.names...); exists {
			value, valueErr := grokOptionalTimestamp(raw)
			if valueErr != nil {
				return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
			}
			*field.target = value
			*field.presence = true
			presence.credits = true
		}
	}
	if !presence.usage && unifiedBilling && grokConfirmedActiveWeeklyPeriod(billing, now) {
		zero := 0.0
		billing.UsagePercent = &zero
		presence.account = true
		presence.usage = true
	}
	if !presence.account && !presence.surface && !presence.credits {
		return GrokBillingObservation{}, grokBillingPresence{}, ErrGrokAccountObservationPayloadInvalid
	}
	return billing, presence, nil
}

func grokConfirmedActiveWeeklyPeriod(billing GrokBillingObservation, now time.Time) bool {
	periodType := strings.ToLower(strings.TrimSpace(billing.PeriodType))
	if periodType != "weekly" && !strings.HasSuffix(periodType, "_weekly") {
		return false
	}
	periodStart, errStart := time.Parse(time.RFC3339, billing.PeriodStart)
	periodEnd, errEnd := time.Parse(time.RFC3339, billing.PeriodEnd)
	billingStart, errBillingStart := time.Parse(time.RFC3339, billing.BillingPeriodStart)
	billingEnd, errBillingEnd := time.Parse(time.RFC3339, billing.BillingPeriodEnd)
	return errStart == nil && errEnd == nil && errBillingStart == nil && errBillingEnd == nil &&
		periodEnd.After(now) && periodEnd.After(periodStart) &&
		periodStart.Equal(billingStart) && periodEnd.Equal(billingEnd)
}

func decodeGrokJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value map[string]json.RawMessage
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("invalid object")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}

func decodeGrokPeriod(raw json.RawMessage) ([3]string, bool, error) {
	var result [3]string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return result, false, nil
	}
	value, err := decodeGrokJSONObject(raw)
	if err != nil {
		return result, false, err
	}
	for index, names := range [][]string{{"type"}, {"start"}, {"end"}} {
		field, exists := grokRawField(value, names...)
		if !exists {
			continue
		}
		text, textErr := grokOptionalString(field)
		if textErr != nil {
			return result, false, textErr
		}
		if index > 0 && text != "" {
			if _, parseErr := time.Parse(time.RFC3339, text); parseErr != nil {
				return result, false, parseErr
			}
		}
		result[index] = text
	}
	return result, result[0] != "" || result[1] != "" || result[2] != "", nil
}

func decodeGrokProductUsage(raw json.RawMessage) ([]GrokProductUsage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []GrokProductUsage{}, nil
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || len(values) > maxGrokProductUsage {
		return nil, errors.New("invalid product usage")
	}
	result := make([]GrokProductUsage, 0, len(values))
	for _, value := range values {
		productRaw, exists := grokRawField(value, "product")
		if !exists {
			return nil, errors.New("missing product")
		}
		product, err := grokOptionalString(productRaw)
		if err != nil || product == "" {
			return nil, errors.New("invalid product")
		}
		var usage *float64
		if usageRaw, exists := grokRawField(value, "usagePercent", "usage_percent"); exists {
			usage, err = grokOptionalNumber(usageRaw, 100)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, GrokProductUsage{Product: product, UsagePercent: usage})
	}
	return result, nil
}

func grokOptionalCentValue(raw json.RawMessage) (*float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		value, err := decodeGrokJSONObject(trimmed)
		if err != nil {
			return nil, err
		}
		inner, exists := grokRawField(value, "val")
		if !exists {
			return nil, errors.New("missing cent value")
		}
		return grokOptionalNumber(inner, maxGrokSafeNumber)
	}
	return grokOptionalNumber(trimmed, maxGrokSafeNumber)
}

func grokOptionalNumber(raw json.RawMessage, maximum float64) (*float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var value float64
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) != nil {
			return nil, errors.New("invalid number")
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, err
		}
		value = parsed
	} else if json.Unmarshal(trimmed, &value) != nil {
		return nil, errors.New("invalid number")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > maximum {
		return nil, errors.New("number out of range")
	}
	return &value, nil
}

func grokOptionalTimestamp(raw json.RawMessage) (string, error) {
	value, err := grokOptionalString(raw)
	if err != nil || value == "" {
		return value, err
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return "", err
	}
	return value, nil
}

func grokOptionalString(raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", errors.New("invalid string")
	}
	value = strings.TrimSpace(value)
	if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("unsafe string")
	}
	return value, nil
}

func grokRawField(value map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if raw, exists := value[name]; exists {
			return raw, true
		}
	}
	return nil, false
}

func mergeGrokBilling(
	current GrokBillingObservation,
	known grokBillingPresence,
	incoming GrokBillingObservation,
	presence grokBillingPresence,
) (GrokBillingObservation, grokBillingPresence) {
	if presence.period && !known.period {
		current.PeriodType, current.PeriodStart, current.PeriodEnd = incoming.PeriodType, incoming.PeriodStart, incoming.PeriodEnd
		known.period = true
	}
	if presence.usage && !known.usage {
		current.UsagePercent = incoming.UsagePercent
		known.usage = true
	}
	if presence.products && !known.products {
		current.ProductUsage = append([]GrokProductUsage(nil), incoming.ProductUsage...)
		known.products = true
	}
	if presence.monthlyLimit {
		current.MonthlyLimitCents = incoming.MonthlyLimitCents
		known.monthlyLimit = true
	}
	if presence.used {
		current.UsedCents = incoming.UsedCents
		known.used = true
	}
	if presence.onDemandCap {
		current.OnDemandCapCents = incoming.OnDemandCapCents
		known.onDemandCap = true
	}
	if presence.onDemandUsed {
		current.OnDemandUsedCents = incoming.OnDemandUsedCents
		known.onDemandUsed = true
	}
	if presence.billingPeriodStart {
		current.BillingPeriodStart = incoming.BillingPeriodStart
		known.billingPeriodStart = true
	}
	if presence.billingPeriodEnd {
		current.BillingPeriodEnd = incoming.BillingPeriodEnd
		known.billingPeriodEnd = true
	}
	known.account = known.account || presence.account
	known.surface = known.surface || presence.surface
	known.credits = known.credits || presence.credits
	return current, known
}

func classifyGrokObservationFailure(results []grokBillingResult) error {
	allHTTP, commonStatus := true, 0
	authFailures, unauthorized := 0, false
	payloadInvalid := false
	for _, result := range results {
		payloadInvalid = payloadInvalid || errors.Is(result.err, ErrGrokAccountObservationPayloadInvalid)
		var upstream *GrokUpstreamHTTPError
		if !errors.As(result.err, &upstream) {
			allHTTP = false
			continue
		}
		if upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden {
			authFailures++
			unauthorized = unauthorized || upstream.StatusCode == http.StatusUnauthorized
		}
		if commonStatus == 0 {
			commonStatus = upstream.StatusCode
		} else if commonStatus != upstream.StatusCode {
			allHTTP = false
		}
	}
	if authFailures == len(results) {
		status := http.StatusForbidden
		if unauthorized {
			status = http.StatusUnauthorized
		}
		return &GrokUpstreamHTTPError{Operation: "billing", StatusCode: status}
	}
	if allHTTP && commonStatus != 0 {
		return &GrokUpstreamHTTPError{Operation: "billing", StatusCode: commonStatus}
	}
	if payloadInvalid {
		return ErrGrokAccountObservationPayloadInvalid
	}
	return ErrGrokAccountObservationUnavailable
}

func grokCredentialTier(credential GrokCredential) *int {
	for _, token := range []string{credential.IDToken, credential.AccessToken} {
		parts := strings.Split(strings.TrimSpace(token), ".")
		if len(parts) < 2 {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims map[string]any
		err = json.Unmarshal(payload, &claims)
		clear(payload)
		if err != nil {
			continue
		}
		for key, raw := range claims {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized != "tier" && !strings.HasSuffix(normalized, "/tier") && !strings.HasSuffix(normalized, ":tier") {
				continue
			}
			value, err := strconv.Atoi(fmt.Sprint(raw))
			if err == nil && value >= 0 && value <= 100 {
				return &value
			}
		}
	}
	return nil
}
