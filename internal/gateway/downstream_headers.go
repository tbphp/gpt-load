package gateway

import (
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/state"
)

// DownstreamHeadersMiddleware applies the system-owned response-header and
// browser-access policy to data-plane namespaces before route authentication.
func (handler *Handler) DownstreamHeadersMiddleware() gin.HandlerFunc {
	return func(context *gin.Context) {
		if context == nil {
			return
		}
		if context.Request == nil || context.Request.URL == nil ||
			!isDataPlaneNamespacePath(context.Request.URL.Path) ||
			handler == nil || handler.manager == nil {
			context.Next()
			return
		}

		snapshot := handler.manager.Current()
		if snapshot == nil {
			context.Next()
			return
		}

		corsHeaders, vary, preflightAllowed := downstreamCORSHeaders(
			context.Request,
			snapshot.Settings.CORS,
		)
		responseRules := snapshot.Settings.ResponseHeaderRules
		if !preflightAllowed && len(corsHeaders) == 0 &&
			len(responseRules.Set) == 0 && len(responseRules.Remove) == 0 {
			context.Next()
			return
		}
		writer := &downstreamHeaderWriter{
			ResponseWriter: context.Writer,
			rules:          responseRules,
			corsHeaders:    corsHeaders,
			vary:           vary,
		}
		context.Writer = writer
		defer writer.apply()

		if preflightAllowed {
			context.AbortWithStatus(http.StatusNoContent)
			return
		}
		context.Next()
	}
}

func isDataPlaneNamespacePath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") ||
		path == "/v1beta" || strings.HasPrefix(path, "/v1beta/")
}

func downstreamCORSHeaders(
	request *http.Request,
	config state.CORSConfig,
) (http.Header, []string, bool) {
	if request == nil || !config.Enabled {
		return nil, nil, false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	allowedOrigin, wildcard := matchCORSOrigin(origin, config.AllowedOrigins)
	preflight := request.Method == http.MethodOptions &&
		origin != "" &&
		strings.TrimSpace(request.Header.Get("Access-Control-Request-Method")) != ""
	if !allowedOrigin {
		return nil, nil, false
	}

	if preflight {
		method := strings.ToUpper(strings.TrimSpace(
			request.Header.Get("Access-Control-Request-Method"),
		))
		requestedHeaders, valid := parseCORSRequestHeaders(
			request.Header.Get("Access-Control-Request-Headers"),
		)
		if !valid || !corsMethodAllowed(method, config.AllowedMethods) ||
			!corsHeadersAllowed(requestedHeaders, config.AllowedHeaders) {
			return nil, nil, false
		}
		headers := corsActualResponseHeaders(origin, wildcard, config)
		headers.Set("Access-Control-Allow-Methods", strings.Join(config.AllowedMethods, ", "))
		if containsExactString(config.AllowedHeaders, "*") {
			if len(requestedHeaders) > 0 {
				headers.Set("Access-Control-Allow-Headers", strings.Join(requestedHeaders, ", "))
			} else {
				headers.Set("Access-Control-Allow-Headers", "*")
			}
		} else {
			headers.Set("Access-Control-Allow-Headers", strings.Join(config.AllowedHeaders, ", "))
		}
		if config.MaxAgeSeconds > 0 {
			headers.Set("Access-Control-Max-Age", strconv.Itoa(config.MaxAgeSeconds))
		}
		vary := []string{"Access-Control-Request-Method", "Access-Control-Request-Headers"}
		if !wildcard {
			vary = append([]string{"Origin"}, vary...)
		}
		return headers, vary, true
	}

	headers := corsActualResponseHeaders(origin, wildcard, config)
	if wildcard {
		return headers, nil, false
	}
	return headers, []string{"Origin"}, false
}

func corsActualResponseHeaders(origin string, wildcard bool, config state.CORSConfig) http.Header {
	headers := make(http.Header)
	if wildcard {
		headers.Set("Access-Control-Allow-Origin", "*")
	} else {
		headers.Set("Access-Control-Allow-Origin", origin)
	}
	if config.AllowCredentials {
		headers.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(config.ExposedHeaders) > 0 {
		headers.Set("Access-Control-Expose-Headers", strings.Join(config.ExposedHeaders, ", "))
	}
	return headers
}

func matchCORSOrigin(origin string, allowed []string) (bool, bool) {
	if origin == "" {
		return false, false
	}
	for _, candidate := range allowed {
		if candidate == "*" {
			return true, true
		}
		if candidate == origin {
			return true, false
		}
	}
	return false, false
}

func corsMethodAllowed(method string, allowed []string) bool {
	if method == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == method {
			return true
		}
	}
	return false
}

func parseCORSRequestHeaders(value string) ([]string, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, true
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if !validCORSRequestHeaderName(name) {
			return nil, false
		}
		identity := strings.ToLower(name)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, textproto.CanonicalMIMEHeaderKey(name))
	}
	return result, true
}

func validCORSRequestHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		value := name[index]
		switch {
		case value >= '0' && value <= '9':
		case value >= 'a' && value <= 'z':
		case value >= 'A' && value <= 'Z':
		case strings.IndexByte("!#$%&'*+-.^_`|~", value) >= 0:
		default:
			return false
		}
	}
	return true
}

func corsHeadersAllowed(requested, allowed []string) bool {
	if containsExactString(allowed, "*") {
		return true
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[strings.ToLower(name)] = struct{}{}
	}
	for _, name := range requested {
		if _, exists := allowedSet[strings.ToLower(name)]; !exists {
			return false
		}
	}
	return true
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type downstreamHeaderWriter struct {
	gin.ResponseWriter
	rules       state.HeaderRules
	corsHeaders http.Header
	vary        []string
	applied     bool
}

func (writer *downstreamHeaderWriter) WriteHeader(statusCode int) {
	writer.apply()
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *downstreamHeaderWriter) WriteHeaderNow() {
	writer.apply()
	writer.ResponseWriter.WriteHeaderNow()
}

func (writer *downstreamHeaderWriter) Write(data []byte) (int, error) {
	writer.apply()
	return writer.ResponseWriter.Write(data)
}

func (writer *downstreamHeaderWriter) WriteString(data string) (int, error) {
	writer.apply()
	return writer.ResponseWriter.WriteString(data)
}

func (writer *downstreamHeaderWriter) Flush() {
	writer.apply()
	writer.ResponseWriter.Flush()
}

func (writer *downstreamHeaderWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *downstreamHeaderWriter) apply() {
	if writer == nil || writer.ResponseWriter == nil || writer.applied {
		return
	}
	writer.applied = true
	headers := writer.ResponseWriter.Header()
	for _, name := range writer.rules.Remove {
		deleteHeaderField(headers, name)
	}
	for name, value := range writer.rules.Set {
		deleteHeaderField(headers, name)
		headers.Set(name, value)
	}
	for name, values := range writer.corsHeaders {
		deleteHeaderField(headers, name)
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	for _, token := range writer.vary {
		appendVaryToken(headers, token)
	}
}

func appendVaryToken(headers http.Header, token string) {
	values := headerFieldValues(headers, "Vary")
	for _, value := range values {
		for _, current := range strings.Split(value, ",") {
			current = strings.TrimSpace(current)
			if current == "*" || strings.EqualFold(current, token) {
				return
			}
		}
	}
	deleteHeaderField(headers, "Vary")
	for _, value := range values {
		headers.Add("Vary", value)
	}
	headers.Add("Vary", token)
}
