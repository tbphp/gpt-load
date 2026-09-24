package codexrouting

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRefreshBefore = 30 * time.Second
	defaultInterval      = 2 * time.Minute
	defaultEventLimit    = 200
	defaultMaxRotates    = 24
	defaultTargetGateway = "unified-80,unified-101,unified-191"
	defaultTicketTTL     = 240 * time.Second
	jarFileName          = "codex-cookies.enc"
)

// Config is process-local. It is not part of the published settings schema;
// sidecar experiments opt in through environment variables.
type Config struct {
	Enabled       bool
	ProbeProxy    string
	ProbeRegions  []string
	EdgeIP        string
	Models        []string
	TargetGateway string
	Candy         bool
	Mint          bool
	Transparent   bool
	TicketLen     int
	TicketTTL     time.Duration
	MaxRotates    int
	RefreshBefore time.Duration
	Interval      time.Duration
	EventLimit    int
}

func LoadConfigFromEnv() Config {
	models := []string{"gpt-6-astra"}
	if raw := strings.TrimSpace(os.Getenv("CODEX_ROUTING_PROBE_MODELS")); raw != "" {
		models = nil
		for _, part := range strings.Split(raw, ",") {
			if model := strings.TrimSpace(part); model != "" {
				models = append(models, model)
			}
		}
		if len(models) == 0 {
			models = []string{"gpt-6-astra"}
		}
	}
	return Config{
		Enabled:       envTruthy("CODEX_ROUTING_ENABLED"),
		ProbeProxy:    strings.TrimSpace(os.Getenv("CODEX_ROUTING_PROBE_PROXY")),
		ProbeRegions:  parseList(os.Getenv("CODEX_ROUTING_PROBE_REGIONS"), []string{"Rand"}),
		EdgeIP:        strings.TrimSpace(os.Getenv("CODEX_ROUTING_EDGE_IP")),
		Models:        models,
		TargetGateway: parseTargetGateways(os.Getenv("CODEX_ROUTING_TARGET_GATEWAY"), defaultTargetGateway),
		Candy:         envTruthy("CODEX_ROUTING_PROBE_CANDY"),
		Mint:          envTruthy("CODEX_ROUTING_MINT"),
		Transparent:   envDefaultTrue("CODEX_ROUTING_TRANSPARENT"),
		TicketLen:     envInt("CODEX_ROUTING_TICKET_LEN", 0),
		TicketTTL:     envDurationSeconds("CODEX_ROUTING_TICKET_TTL_SECONDS", defaultTicketTTL),
		MaxRotates:    envInt("CODEX_ROUTING_PROBE_MAX_ROTATES", defaultMaxRotates),
		RefreshBefore: envDurationSeconds("CODEX_ROUTING_REFRESH_BEFORE_SECONDS", defaultRefreshBefore),
		Interval:      envDurationSeconds("CODEX_ROUTING_PROBE_INTERVAL_SECONDS", defaultInterval),
		EventLimit:    defaultEventLimit,
	}
}

func envTruthy(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envDefaultTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func parseList(raw string, fallback []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if len(value) == 2 {
			value = strings.ToUpper(value)
		}
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return append([]string(nil), fallback...)
	}
	return out
}

func parseTargetGateways(raw, fallback string) string {
	var out []string
	seen := make(map[string]struct{})
	source := raw
	if strings.TrimSpace(source) == "" {
		source = fallback
	}
	for _, part := range strings.Split(source, ",") {
		value := normalizeGateway(part)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return strings.Join(out, ",")
}

func gatewayAllowed(targets, region string) bool {
	region = normalizeGateway(region)
	if region == "" {
		return false
	}
	if strings.TrimSpace(targets) == "" {
		return true
	}
	for _, part := range strings.Split(targets, ",") {
		if normalizeGateway(part) == region {
			return true
		}
	}
	return false
}

func envDurationSeconds(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
