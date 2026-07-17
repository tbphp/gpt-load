package health

type ErrorClass uint8

const (
	ErrorClassNonRetryable ErrorClass = iota
	ErrorClassRetryable
)

func (c ErrorClass) IsRetryable() bool {
	return c == ErrorClassRetryable
}
