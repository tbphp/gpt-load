package health

type FailureCategory uint8

const (
	FailureCategoryAmbiguous FailureCategory = iota
	FailureCategoryOK
	FailureCategoryRateLimited
	FailureCategoryModelUnavailable
	FailureCategoryInvalidKey
	FailureCategoryUpstreamHostError
	FailureCategoryClientError
	FailureCategoryDownstreamCancel
)
