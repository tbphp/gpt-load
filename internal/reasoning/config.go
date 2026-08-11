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
