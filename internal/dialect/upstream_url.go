package dialect

import (
	"fmt"
	"net/url"
	"strings"
)

func resolveUpstreamURL(baseURL, resourcePath, rawQuery string) (string, error) {
	if resourcePath == "" || !strings.HasPrefix(resourcePath, "/") || strings.HasPrefix(resourcePath, "//") {
		return "", fmt.Errorf("upstream resource path must start with one slash")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") &&
			!strings.EqualFold(parsed.Scheme, "https")) ||
		parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid upstream base URL")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + resourcePath
	parsed.RawPath = ""
	if parsed.RawQuery == "" {
		parsed.RawQuery = rawQuery
	} else if rawQuery != "" {
		parsed.RawQuery += "&" + rawQuery
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}
