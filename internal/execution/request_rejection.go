package execution

import "strings"

// ExplicitRequestRejection 仅识别具体请求错误代码，不以通用错误类型或消息子串终止重试。
func ExplicitRequestRejection(typeValue, codeValue string) bool {
	for _, value := range []string{typeValue, codeValue} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "context_length_exceeded", "invalid_parameter", "content_policy_violation":
			return true
		}
	}
	return false
}
