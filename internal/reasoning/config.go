package reasoning

// Config is the explicit reasoning configuration supplied by the client.
// Empty fields mean the client did not provide that part of the configuration.
type Config struct {
	Mode         string
	Effort       string
	BudgetTokens *int64
}

func (config Config) Present() bool {
	return config.Mode != "" || config.Effort != "" || config.BudgetTokens != nil
}
