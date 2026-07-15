package utils

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HeaderRule describes a request-header mutation without depending on business models.
type HeaderRule struct {
	Action string
	Key    string
	Value  string
}

// HeaderVariableContext contains the values available to header templates.
type HeaderVariableContext struct {
	ClientIP  string
	GroupName string
	APIKey    string
}

// ResolveHeaderVariables resolves dynamic variables in a header value.
func ResolveHeaderVariables(value string, ctx *HeaderVariableContext) string {
	if ctx == nil {
		return value
	}

	now := time.Now()
	variables := map[string]string{
		"${CLIENT_IP}":    ctx.ClientIP,
		"${TIMESTAMP_MS}": strconv.FormatInt(now.UnixMilli(), 10),
		"${TIMESTAMP_S}":  strconv.FormatInt(now.Unix(), 10),
		"${GROUP_NAME}":   ctx.GroupName,
		"${API_KEY}":      ctx.APIKey,
	}

	result := value
	for variable, replacement := range variables {
		result = strings.ReplaceAll(result, variable, replacement)
	}
	return result
}

// ApplyHeaderRules applies header mutations to an HTTP request.
func ApplyHeaderRules(req *http.Request, rules []HeaderRule, ctx *HeaderVariableContext) {
	if req == nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		canonicalKey := http.CanonicalHeaderKey(rule.Key)
		switch rule.Action {
		case "remove":
			req.Header.Del(canonicalKey)
		case "set":
			req.Header.Set(canonicalKey, ResolveHeaderVariables(rule.Value, ctx))
		}
	}
}
