package state

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/textproto"
	"strings"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/httpheader"
)

const (
	SettingConnectTimeout          = "connect_timeout"
	SettingFirstByteTimeout        = "first_byte_timeout"
	SettingRequestTimeout          = "request_timeout"
	SettingStreamIdleTimeout       = "stream_idle_timeout"
	SettingHeaderRules             = "header_rules"
	SettingInjectUsageOptions      = "inject_usage_options"
	SettingRequestLogRetentionDays = "request_log_retention_days"
)

const (
	defaultRequestLogRetentionDays = 7
	minRequestLogRetentionDays     = 1
	maxRequestLogRetentionDays     = 365
)

type RuntimeSettings struct {
	ConnectTimeout          time.Duration
	FirstByteTimeout        time.Duration
	RequestTimeout          time.Duration
	StreamIdleTimeout       time.Duration
	HeaderRules             HeaderRules
	InjectUsageOptions      bool
	RequestLogRetentionDays int
}

type ResolvedGroupSettings struct {
	Timeouts           TimeoutConfig
	HeaderRules        HeaderRules
	InjectUsageOptions bool
}

func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		ConnectTimeout:          15 * time.Second,
		FirstByteTimeout:        120 * time.Second,
		RequestTimeout:          600 * time.Second,
		StreamIdleTimeout:       300 * time.Second,
		HeaderRules:             HeaderRules{Set: map[string]string{}},
		InjectUsageOptions:      true,
		RequestLogRetentionDays: defaultRequestLogRetentionDays,
	}
}

func IsRuntimeSettingKey(key string) bool {
	switch key {
	case SettingConnectTimeout,
		SettingFirstByteTimeout,
		SettingRequestTimeout,
		SettingStreamIdleTimeout,
		SettingHeaderRules,
		SettingInjectUsageOptions,
		SettingRequestLogRetentionDays:
		return true
	default:
		return false
	}
}

func ResolveRuntimeSettings(settings config.Settings) (RuntimeSettings, error) {
	resolved := DefaultRuntimeSettings()
	for key, value := range settings {
		switch key {
		case SettingConnectTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.ConnectTimeout = time.Duration(seconds) * time.Second
		case SettingFirstByteTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.FirstByteTimeout = time.Duration(seconds) * time.Second
		case SettingRequestTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.RequestTimeout = time.Duration(seconds) * time.Second
		case SettingStreamIdleTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.StreamIdleTimeout = time.Duration(seconds) * time.Second
		case SettingHeaderRules:
			rules, err := parseHeaderRules(value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.HeaderRules = rules
		case SettingInjectUsageOptions:
			value, err := strictBoolean(key, value)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.InjectUsageOptions = value
		case SettingRequestLogRetentionDays:
			days, err := wholeNumberInRange(
				key,
				value,
				minRequestLogRetentionDays,
				maxRequestLogRetentionDays,
			)
			if err != nil {
				return RuntimeSettings{}, err
			}
			resolved.RequestLogRetentionDays = days
		default:
			return RuntimeSettings{}, fmt.Errorf("unknown runtime setting %q", key)
		}
	}
	return resolved, nil
}

func ResolveGroupRuntimeSettings(
	base RuntimeSettings,
	settings config.Settings,
) (ResolvedGroupSettings, error) {
	resolved := ResolvedGroupSettings{
		Timeouts: TimeoutConfig{
			Connect:    base.ConnectTimeout,
			FirstByte:  base.FirstByteTimeout,
			Request:    base.RequestTimeout,
			StreamIdle: base.StreamIdleTimeout,
		},
		HeaderRules:        cloneHeaderRules(base.HeaderRules),
		InjectUsageOptions: base.InjectUsageOptions,
	}
	for key, value := range settings {
		switch key {
		case SettingConnectTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.Timeouts.Connect = time.Duration(seconds) * time.Second
		case SettingFirstByteTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.Timeouts.FirstByte = time.Duration(seconds) * time.Second
		case SettingRequestTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.Timeouts.Request = time.Duration(seconds) * time.Second
		case SettingStreamIdleTimeout:
			seconds, err := positiveWholeSeconds(key, value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.Timeouts.StreamIdle = time.Duration(seconds) * time.Second
		case SettingHeaderRules:
			parsed, err := parseHeaderRules(value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.HeaderRules = parsed
		case SettingInjectUsageOptions:
			parsed, err := strictBoolean(key, value)
			if err != nil {
				return ResolvedGroupSettings{}, err
			}
			resolved.InjectUsageOptions = parsed
		default:
			return ResolvedGroupSettings{}, fmt.Errorf("unknown group setting %q", key)
		}
	}
	return resolved, nil
}

func ValidateRuntimeSetting(key string, value any) error {
	switch key {
	case SettingConnectTimeout,
		SettingFirstByteTimeout,
		SettingRequestTimeout,
		SettingStreamIdleTimeout:
		_, err := positiveWholeSeconds(key, value)
		return err
	case SettingHeaderRules:
		_, err := parseHeaderRules(value)
		return err
	case SettingInjectUsageOptions:
		_, err := strictBoolean(key, value)
		return err
	case SettingRequestLogRetentionDays:
		_, err := wholeNumberInRange(
			key,
			value,
			minRequestLogRetentionDays,
			maxRequestLogRetentionDays,
		)
		return err
	default:
		return fmt.Errorf("unknown runtime setting %q", key)
	}
}

func strictBoolean(path string, value any) (bool, error) {
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", path)
	}
	return parsed, nil
}

func wholeNumberInRange(path string, value any, minimum, maximum int) (int, error) {
	var number int64
	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int64:
		number = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed ||
			typed < float64(minimum) || typed > float64(maximum) {
			return 0, fmt.Errorf("%s must be a whole number between %d and %d", path, minimum, maximum)
		}
		number = int64(typed)
	case json.Number:
		literal := typed.String()
		parsed, ok := new(big.Rat).SetString(literal)
		if !json.Valid([]byte(literal)) || !ok || !parsed.IsInt() || !parsed.Num().IsInt64() {
			return 0, fmt.Errorf("%s must be a whole number between %d and %d", path, minimum, maximum)
		}
		number = parsed.Num().Int64()
	default:
		return 0, fmt.Errorf("%s must be a whole number between %d and %d", path, minimum, maximum)
	}
	if number < int64(minimum) || number > int64(maximum) {
		return 0, fmt.Errorf("%s must be a whole number between %d and %d", path, minimum, maximum)
	}
	return int(number), nil
}

func positiveWholeSeconds(path string, value any) (int64, error) {
	const maxTimeoutSeconds = int64((1<<63)-1) / int64(time.Second)

	var seconds int64
	switch typed := value.(type) {
	case int:
		seconds = int64(typed)
	case int64:
		seconds = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed > float64(maxTimeoutSeconds) {
			return 0, fmt.Errorf("%s must be a positive whole number", path)
		}
		seconds = int64(typed)
	case json.Number:
		literal := typed.String()
		parsed, ok := new(big.Rat).SetString(literal)
		if !json.Valid([]byte(literal)) || !ok || !parsed.IsInt() || !parsed.Num().IsInt64() {
			return 0, fmt.Errorf("%s must be a positive whole number", path)
		}
		seconds = parsed.Num().Int64()
	default:
		return 0, fmt.Errorf("%s must be a positive whole number", path)
	}
	if seconds <= 0 || seconds > maxTimeoutSeconds {
		return 0, fmt.Errorf("%s must be a positive whole number within duration range", path)
	}
	return seconds, nil
}

func parseHeaderRules(value any) (HeaderRules, error) {
	rules := HeaderRules{Set: make(map[string]string)}
	object, ok := value.(map[string]any)
	if !ok {
		return HeaderRules{}, fmt.Errorf("header_rules must be an object")
	}
	for key := range object {
		if key != "set" && key != "remove" {
			return HeaderRules{}, fmt.Errorf("unknown header_rules field %q", key)
		}
	}
	seen := make(map[string]struct{})
	if rawSet, exists := object["set"]; exists {
		set, ok := rawSet.(map[string]any)
		if !ok {
			return HeaderRules{}, fmt.Errorf("header_rules.set must be an object")
		}
		for name, rawValue := range set {
			if !validHTTPHeaderName(name) {
				return HeaderRules{}, fmt.Errorf("header_rules.set contains invalid header name %q", name)
			}
			canonicalName := textproto.CanonicalMIMEHeaderKey(name)
			if httpheader.IsForbiddenRequestRuleSetName(canonicalName) {
				return HeaderRules{}, fmt.Errorf(
					"header_rules.set cannot set forbidden header %q",
					canonicalName,
				)
			}
			identity := strings.ToLower(name)
			if _, duplicate := seen[identity]; duplicate {
				return HeaderRules{}, fmt.Errorf(
					"header_rules.set contains duplicate header %q",
					canonicalName,
				)
			}
			seen[identity] = struct{}{}
			text, ok := rawValue.(string)
			if !ok {
				return HeaderRules{}, fmt.Errorf("header_rules.set.%s must be a string", name)
			}
			if !validHTTPHeaderValue(text) {
				return HeaderRules{}, fmt.Errorf("header_rules.set.%s contains invalid header value", name)
			}
			if httpheader.IsCredentialName(canonicalName) &&
				!strings.Contains(text, "${API_KEY}") {
				return HeaderRules{}, fmt.Errorf(
					"header_rules.set.%s credential value must contain ${API_KEY}",
					name,
				)
			}
			rules.Set[canonicalName] = text
		}
	}
	if rawRemove, exists := object["remove"]; exists {
		remove, ok := rawRemove.([]any)
		if !ok {
			return HeaderRules{}, fmt.Errorf("header_rules.remove must be an array")
		}
		rules.Remove = make([]string, 0, len(remove))
		for index, rawName := range remove {
			name, ok := rawName.(string)
			if !ok {
				return HeaderRules{}, fmt.Errorf("header_rules.remove[%d] must be a string", index)
			}
			if !validHTTPHeaderName(name) {
				return HeaderRules{}, fmt.Errorf(
					"header_rules.remove[%d] contains invalid header name %q",
					index,
					name,
				)
			}
			canonicalName := textproto.CanonicalMIMEHeaderKey(name)
			identity := strings.ToLower(name)
			if _, duplicate := seen[identity]; duplicate {
				return HeaderRules{}, fmt.Errorf(
					"header_rules.remove contains duplicate header %q",
					canonicalName,
				)
			}
			seen[identity] = struct{}{}
			rules.Remove = append(rules.Remove, canonicalName)
		}
	}
	return rules, nil
}

func cloneHeaderRules(source HeaderRules) HeaderRules {
	cloned := HeaderRules{Set: make(map[string]string, len(source.Set))}
	for name, value := range source.Set {
		cloned.Set[name] = value
	}
	cloned.Remove = append([]string(nil), source.Remove...)
	return cloned
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		if !isHTTPTokenByte(name[index]) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(value byte) bool {
	switch {
	case value >= '0' && value <= '9':
		return true
	case value >= 'a' && value <= 'z':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	default:
		return strings.IndexByte("!#$%&'*+-.^_`|~", value) >= 0
	}
}

func validHTTPHeaderValue(value string) bool {
	for index := range len(value) {
		character := value[index]
		if (character < ' ' && character != '\t') || character == 0x7f {
			return false
		}
	}
	return true
}
