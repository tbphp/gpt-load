package utils

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPResolver 在启动时编译，请求期间只读。空代理名单表示显式信任所配置的请求头。
type ClientIPResolver struct {
	header  string
	trusted []netip.Prefix
}

type clientIPContextKey struct{}
type clientIPResult struct {
	address string
	err     error
}

func NewClientIPResolver(header string, trusted []string) (*ClientIPResolver, error) {
	header = strings.TrimSpace(header)
	for _, char := range header {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", char) {
			continue
		}
		return nil, fmt.Errorf("CLIENT_IP_HEADER must be a valid HTTP header name")
	}
	resolver := &ClientIPResolver{header: http.CanonicalHeaderKey(header)}
	for _, value := range trusted {
		prefix, err := normalizeAllowedCIDR(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES must contain IP addresses or CIDRs")
		}
		resolver.trusted = append(resolver.trusted, prefix)
	}
	return resolver, nil
}

func (resolver *ClientIPResolver) Apply(request *http.Request) *http.Request {
	if request == nil {
		return nil
	}
	address, err := resolver.resolve(request)
	return request.WithContext(context.WithValue(request.Context(), clientIPContextKey{}, clientIPResult{address: address, err: err}))
}

// ClientIP 返回入口冻结的来源地址；未经过进程入口的内部请求只使用连接地址。
func ClientIP(request *http.Request) (string, error) {
	if request == nil {
		return "", fmt.Errorf("client IP request is missing")
	}
	if result, ok := request.Context().Value(clientIPContextKey{}).(clientIPResult); ok {
		return result.address, result.err
	}
	return NormalizePeerIP(request.RemoteAddr)
}

func (resolver *ClientIPResolver) resolve(request *http.Request) (string, error) {
	peer, peerErr := NormalizePeerIP(request.RemoteAddr)
	if resolver == nil || resolver.header == "" || len(resolver.trusted) > 0 && !AllowedCIDRsContain(resolver.trusted, peer) {
		return peer, peerErr
	}
	value := strings.Join(request.Header.Values(resolver.header), ",")
	if resolver.header != "X-Forwarded-For" {
		if address, err := NormalizeIP(value); err == nil {
			return address, nil
		}
		return peer, peerErr
	}
	items := strings.Split(value, ",")
	if len(resolver.trusted) == 0 {
		if address, err := NormalizeIP(items[0]); err == nil {
			return address, nil
		}
		return peer, peerErr
	}
	for index := len(items) - 1; index >= 0; index-- {
		address, err := NormalizeIP(items[index])
		if err != nil {
			break
		}
		if index == 0 || !AllowedCIDRsContain(resolver.trusted, address) {
			return address, nil
		}
	}
	return peer, peerErr
}

// NormalizeIP 供请求头和精确搜索共用，不接受端口、地址列表或接口区域名。
func NormalizeIP(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || address.Zone() != "" {
		return "", fmt.Errorf("invalid IP address")
	}
	return address.Unmap().String(), nil
}
