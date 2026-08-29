package gateway

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

type keyHasher interface {
	Hash(string) string
}

type accessKeyAuthFailureReason string

const (
	accessKeyAuthFailureInvalid        accessKeyAuthFailureReason = "invalid_access_key"
	accessKeyAuthFailureExpired        accessKeyAuthFailureReason = "access_key_expired"
	accessKeyAuthFailurePeerNotAllowed accessKeyAuthFailureReason = "peer_not_allowed"
)

func authenticate(
	request *http.Request,
	snapshot *state.ConfigSnapshot,
	hasher keyHasher,
	requestStarted time.Time,
) (state.AccessKeyView, bool, accessKeyAuthFailureReason) {
	if request == nil || snapshot == nil || hasher == nil {
		return state.AccessKeyView{}, false, accessKeyAuthFailureInvalid
	}
	plaintext := extractClientKey(request)
	if plaintext == "" {
		return state.AccessKeyView{}, false, accessKeyAuthFailureInvalid
	}
	accessKey, ok := snapshot.AccessKeysByHash[hasher.Hash(plaintext)]
	if !ok {
		return state.AccessKeyView{}, false, accessKeyAuthFailureInvalid
	}
	if accessKey.ExpiresAtMS != nil && requestStarted.UnixMilli() >= *accessKey.ExpiresAtMS {
		return accessKey, false, accessKeyAuthFailureExpired
	}
	if len(accessKey.AllowedPeerCIDRs) > 0 {
		peer, err := utils.NormalizePeerIP(request.RemoteAddr)
		if err != nil || !utils.AllowedCIDRsContain(accessKey.AllowedPeerCIDRs, peer) {
			return accessKey, false, accessKeyAuthFailurePeerNotAllowed
		}
	}
	return accessKey, true, ""
}

func extractClientKey(request *http.Request) string {
	if request == nil {
		return ""
	}

	queryKey := ""
	if request.URL != nil {
		queryKeySet := false
		segments := strings.Split(request.URL.RawQuery, "&")
		forwarded := make([]string, 0, len(segments))
		for _, segment := range segments {
			rawName, rawValue, _ := strings.Cut(segment, "=")
			name, err := url.QueryUnescape(rawName)
			if err != nil || name != "key" {
				forwarded = append(forwarded, segment)
				continue
			}
			if !queryKeySet {
				value, err := url.QueryUnescape(rawValue)
				if err == nil {
					queryKey = strings.TrimSpace(value)
					queryKeySet = true
				}
			}
		}
		request.URL.RawQuery = strings.Join(forwarded, "&")
	}

	fields := strings.Fields(request.Header.Get("Authorization"))
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") && fields[1] != "" {
		return fields[1]
	}
	for _, name := range []string{"x-api-key", "x-goog-api-key"} {
		if value := strings.TrimSpace(request.Header.Get(name)); value != "" {
			return value
		}
	}
	return queryKey
}
