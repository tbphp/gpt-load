package reasoning

// Config is the explicit reasoning configuration supplied by the client.
// Empty fields mean the client did not provide that part of the configuration.
type Config struct {
	Mode         string `json:"mode,omitempty"`
	Effort       string `json:"effort,omitempty"`
	BudgetTokens *int64 `json:"budget_tokens,omitempty"`
}

// Clone returns an independent reasoning configuration.
func (config Config) Clone() Config {
	clone := config
	if config.BudgetTokens != nil {
		value := *config.BudgetTokens
		clone.BudgetTokens = &value
	}
	return clone
}

func (config Config) Present() bool {
	return config.Mode != "" || config.Effort != "" || config.BudgetTokens != nil
}

// RequiresCapability reports whether the request asks the upstream to perform
// reasoning. An explicit "none" setting is a valid opt-out and must not make
// routing require a reasoning-capable target.
func (config Config) RequiresCapability() bool {
	if config.BudgetTokens != nil {
		return true
	}
	if config.Mode != "" && config.Mode != "none" {
		return true
	}
	return config.Effort != "" && config.Effort != "none"
}
