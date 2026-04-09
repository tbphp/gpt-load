package utils

import (
	"gpt-load/internal/models"
	"net/http"
)

// ApplyQueryParamRules applies query parameter rules to the HTTP request URL.
func ApplyQueryParamRules(req *http.Request, rules []models.QueryParamRule, ctx *HeaderVariableContext) {
	if req == nil || len(rules) == 0 {
		return
	}

	q := req.URL.Query()

	for _, rule := range rules {
		switch rule.Action {
		case "remove":
			q.Del(rule.Key)
		case "set":
			resolvedValue := ResolveHeaderVariables(rule.Value, ctx)
			q.Set(rule.Key, resolvedValue)
		}
	}

	req.URL.RawQuery = q.Encode()
}
