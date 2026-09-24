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
	FailureCategoryAuthenticationRequired
)

func (category FailureCategory) Valid() bool {
	return category >= FailureCategoryAmbiguous &&
		category <= FailureCategoryAuthenticationRequired
}

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
	case FailureCategoryAuthenticationRequired:
		return "authentication_required"
	default:
		return "ambiguous"
	}
}

// EmptyResponseCode 标识「上游正常完成但没有产出任何内容」的证据。它由数据面
// 在提交响应前判定，是业务层面的空回，而非传输或协议故障。
const EmptyResponseCode = "upstream_empty_response"
