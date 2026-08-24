// Package outboundproxy defines the provider-neutral outbound proxy policy.
package outboundproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid outbound proxy config")

const SystemSettingKey = "proxy_config"

type Mode string

const (
	ModeInherit     Mode = "inherit"
	ModeDirect      Mode = "direct"
	ModeCustom      Mode = "custom"
	ModeEnvironment Mode = "environment"
)

type Source string

const (
	SourceCredential  Source = "credential"
	SourceGroup       Source = "group"
	SourceGlobal      Source = "global"
	SourceEnvironment Source = "environment"
	SourceDefault     Source = "default"
)

// Config is the persisted proxy override. Inherit is represented by an absent
// persisted value; ModeInherit is retained for control-plane input and views.
type Config struct {
	Mode Mode   `json:"mode"`
	URL  string `json:"url,omitempty"`
}

type Effective struct {
	Config Config `json:"config"`
	Source Source `json:"source"`
}

type View struct {
	ConfiguredMode  Mode   `json:"configured_mode"`
	EffectiveMode   Mode   `json:"effective_mode"`
	EffectiveSource Source `json:"effective_source"`
	DisplayURL      string `json:"display_url,omitempty"`
	HasAuth         bool   `json:"has_auth"`
}

// Environment snapshots whether the process has a standard proxy configured.
// The concrete environment values remain owned by the transport implementation.
func Environment() *Config {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return &Config{Mode: ModeEnvironment}
		}
	}
	return nil
}

func Normalize(input Config) (Config, error) {
	switch input.Mode {
	case ModeInherit, ModeDirect:
		if input.URL != "" {
			return Config{}, ErrInvalidConfig
		}
		return Config{Mode: input.Mode}, nil
	case ModeCustom:
		return normalizeCustom(input.URL)
	default:
		return Config{}, ErrInvalidConfig
	}
}

func normalizeCustom(endpoint string) (Config, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return Config{}, ErrInvalidConfig
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" {
		return Config{}, ErrInvalidConfig
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "http", "socks5":
	default:
		return Config{}, ErrInvalidConfig
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") || (parsed.Scheme == "socks5" && port == "") {
		return Config{}, ErrInvalidConfig
	}
	if port != "" {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || value == 0 {
			return Config{}, ErrInvalidConfig
		}
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return Config{}, ErrInvalidConfig
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" {
		return Config{}, ErrInvalidConfig
	}
	if parsed.User != nil {
		password, hasPassword := parsed.User.Password()
		if parsed.User.Username() == "" || !hasPassword || password == "" {
			return Config{}, ErrInvalidConfig
		}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return Config{Mode: ModeCustom, URL: parsed.String()}, nil
}

// Display returns the safe UI representation of one proxy override.
func Display(config Config) (string, bool, error) {
	normalized, err := Normalize(config)
	if err != nil {
		return "", false, err
	}
	if normalized.Mode != ModeCustom {
		return "", false, nil
	}
	parsed, err := url.Parse(normalized.URL)
	if err != nil {
		return "", false, ErrInvalidConfig
	}
	hasAuth := parsed.User != nil
	if parsed.User != nil {
		userinfo := url.User(parsed.User.Username()).String()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			userinfo += ":******"
		}
		return parsed.Scheme + "://" + userinfo + "@" + parsed.Host, hasAuth, nil
	}
	return parsed.String(), hasAuth, nil
}

// Resolve applies Credential > Group > Global > Environment > Direct.
func Resolve(credential, group, global, environment *Config) (Effective, error) {
	candidates := []struct {
		config *Config
		source Source
	}{
		{config: credential, source: SourceCredential},
		{config: group, source: SourceGroup},
		{config: global, source: SourceGlobal},
		{config: environment, source: SourceEnvironment},
	}
	for _, candidate := range candidates {
		if candidate.config == nil {
			continue
		}
		if candidate.source == SourceEnvironment && candidate.config.Mode == ModeEnvironment && candidate.config.URL == "" {
			return Effective{Config: *candidate.config, Source: candidate.source}, nil
		}
		normalized, err := Normalize(*candidate.config)
		if err != nil {
			return Effective{}, err
		}
		if normalized.Mode == ModeInherit {
			continue
		}
		return Effective{Config: normalized, Source: candidate.source}, nil
	}
	return Effective{Config: Config{Mode: ModeDirect}, Source: SourceDefault}, nil
}

func NormalizeEffective(input Effective) (Effective, error) {
	if input.Config.Mode == "" {
		return Effective{Config: Config{Mode: ModeDirect}, Source: SourceDefault}, nil
	}
	if input.Config.Mode == ModeEnvironment {
		if input.Config.URL != "" {
			return Effective{}, ErrInvalidConfig
		}
		return Effective{Config: Config{Mode: ModeEnvironment}, Source: SourceEnvironment}, nil
	}
	config, err := Normalize(input.Config)
	if err != nil || config.Mode == ModeInherit {
		return Effective{}, ErrInvalidConfig
	}
	source := input.Source
	if source == "" {
		source = SourceDefault
	}
	return Effective{Config: config, Source: source}, nil
}

func NewView(configured *Config, effective Effective) (View, error) {
	configuredMode := ModeInherit
	if configured != nil {
		normalized, err := Normalize(*configured)
		if err != nil {
			return View{}, err
		}
		configuredMode = normalized.Mode
	}
	display, hasAuth, err := Display(effective.Config)
	if effective.Config.Mode == ModeEnvironment {
		display, hasAuth, err = "", false, nil
	}
	if err != nil {
		return View{}, err
	}
	return View{
		ConfiguredMode:  configuredMode,
		EffectiveMode:   effective.Config.Mode,
		EffectiveSource: effective.Source,
		DisplayURL:      display,
		HasAuth:         hasAuth,
	}, nil
}

func Encode(config Config) (string, error) {
	normalized, err := Normalize(config)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", ErrInvalidConfig
	}
	return string(encoded), nil
}

func Decode(encoded string) (Config, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, ErrInvalidConfig
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Config{}, ErrInvalidConfig
	}
	return Normalize(config)
}
