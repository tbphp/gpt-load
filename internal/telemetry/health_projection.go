package telemetry

import "gpt-load/internal/health"

// FailureCategoryOf 把健康判定类别投影为请求日志使用的类别。
// 数据面与控制面共用同一份映射，避免请求日志出现两套类别词汇。
func FailureCategoryOf(value health.FailureCategory) FailureCategory {
	switch value {
	case health.FailureCategoryOK:
		return FailureCategoryOK
	case health.FailureCategoryRateLimited:
		return FailureCategoryRateLimited
	case health.FailureCategoryModelUnavailable:
		return FailureCategoryModelUnavailable
	case health.FailureCategoryInvalidKey:
		return FailureCategoryInvalidKey
	case health.FailureCategoryUpstreamHostError:
		return FailureCategoryUpstreamHost
	case health.FailureCategoryClientError:
		return FailureCategoryClientError
	case health.FailureCategoryConversionUnsupported:
		return FailureCategoryConversionUnsupported
	case health.FailureCategoryDownstreamCancel:
		return FailureCategoryDownstreamCancel
	case health.FailureCategoryAuthenticationRequired:
		return FailureCategoryAuthenticationRequired
	default:
		return FailureCategoryAmbiguous
	}
}

// ActionOf 把健康判定投影为请求日志使用的动作。
func ActionOf(decision health.Decision) Action {
	switch decision.LegacyAction() {
	case health.ActionRetry:
		return ActionRetry
	case health.ActionCooldownCredential:
		return ActionCooldownCredential
	case health.ActionFailCredential:
		return ActionFailCredential
	case health.ActionSkipGroup:
		return ActionSkipGroup
	default:
		return ActionTerminate
	}
}
