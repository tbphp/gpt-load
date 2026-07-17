package dialect

import (
	"net/http"
	"strings"

	"gpt-load/internal/state"
)

type credentialInjector interface {
	InjectCredential(headers http.Header, apiKey string)
}

func ApplyCredential(
	injector credentialInjector,
	headers http.Header,
	apiKey string,
	rules state.HeaderRules,
) {
	if injector == nil || headers == nil {
		return
	}

	injector.InjectCredential(headers, apiKey)
	for name, value := range rules.Set {
		headers.Set(name, strings.ReplaceAll(value, "${API_KEY}", apiKey))
	}
	for _, name := range rules.Remove {
		headers.Del(name)
	}
}
