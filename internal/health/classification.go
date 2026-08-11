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
	FailureCategoryConversionUnsupported
	FailureCategoryDownstreamCancel
)

func (category FailureCategory) String() string {
	switch category {
	case FailureCategoryOK:
		return "ok"
	case FailureCategoryRateLimited:
		return "rate_limited"
	case FailureCategoryModelUnavailable:
		return "model_unavailable"
	case FailureCategoryInvalidKey:
		return "invalid_key"
	case FailureCategoryUpstreamHostError:
		return "upstream_host_error"
	case FailureCategoryClientError:
		return "client_error"
	case FailureCategoryConversionUnsupported:
		return "conversion_unsupported"
	case FailureCategoryDownstreamCancel:
		return "downstream_cancel"
	default:
		return "ambiguous"
	}
}
