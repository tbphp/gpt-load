package demo

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/response"
)

// 只开放已确认不访问外部网络的管理操作，新增 API 默认不在演示环境放行。
var allowedRoutes = map[string]bool{
	"GET /api/auth/session":                                true,
	"GET /api/channels":                                    true,
	"GET /api/home":                                        true,
	"GET /api/home/statistics":                             true,
	"GET /api/home/subscription-accounts":                  true,
	"GET /api/health":                                      true,
	"GET /api/logs":                                        true,
	"GET /api/logs/:request_id":                            true,
	"GET /api/usage":                                       true,
	"GET /api/system/info":                                 true,
	"GET /api/settings":                                    true,
	"PUT /api/settings":                                    true,
	"POST /api/settings/request-redaction/validate":        true,
	"POST /api/route/inspect":                              true,
	"GET /api/models":                                      true,
	"GET /api/models/client-catalog":                       true,
	"PUT /api/models/client-catalog":                       true,
	"POST /api/models/client-catalog/preview":              true,
	"GET /api/models/profile":                              true,
	"PUT /api/models/profile":                              true,
	"GET /api/model-prices":                                true,
	"GET /api/model-prices/:id":                            true,
	"GET /api/model-prices/sources":                        true,
	"PUT /api/model-prices/:id":                            true,
	"DELETE /api/model-prices/:id":                         true,
	"POST /api/model-prices/batch":                         true,
	"POST /api/model-prices/:id/reset":                     true,
	"GET /api/groups":                                      true,
	"POST /api/groups":                                     true,
	"GET /api/groups/options":                              true,
	"GET /api/groups/:group_id":                            true,
	"DELETE /api/groups/:group_id":                         true,
	"GET /api/groups/:group_id/settings":                   true,
	"PUT /api/groups/:group_id/settings":                   true,
	"PUT /api/groups/:group_id/channel":                    true,
	"GET /api/groups/:group_id/models":                     true,
	"PUT /api/groups/:group_id/models":                     true,
	"GET /api/groups/:group_id/credentials":                true,
	"GET /api/groups/:group_id/credentials/:credential_id": true,
	"GET /api/groups/:group_id/credentials/:credential_id/quota-history": true,
	"PUT /api/groups/:group_id/credentials/:credential_id":               true,
	"DELETE /api/groups/:group_id/credentials/:credential_id":            true,
	"POST /api/groups/:group_id/credentials/:credential_id/reveal":       true,
	"POST /api/groups/:group_id/credentials/:credential_id/restore":      true,
	"POST /api/groups/:group_id/credentials/batch":                       true,
	"POST /api/groups/:group_id/credentials/import":                      true,
	"GET /api/access-keys":                                            true,
	"POST /api/access-keys":                                           true,
	"GET /api/access-keys/options":                                    true,
	"PUT /api/access-keys/:id":                                        true,
	"DELETE /api/access-keys/:id":                                     true,
	"POST /api/access-keys/:id/reveal":                                true,
	"POST /api/access-keys/:id/rotate":                                true,
	"POST /api/access-keys/:id/cost-limits/reset":                     true,
	"GET /api/access-keys/:id/rpm":                                    true,
	"GET /api/proxies":                                                true,
	"POST /api/proxies":                                               true,
	"PUT /api/proxies/:id":                                            true,
	"POST /api/proxies/:id/reveal":                                    true,
	"POST /api/proxies/impact":                                        true,
	"POST /api/proxies/batch":                                         true,
	"POST /api/proxies/import":                                        true,
	"GET /api/modern/groups":                                          true,
	"GET /api/modern/groups/usage":                                    true,
	"GET /api/modern/access-keys":                                     true,
	"GET /api/modern/credentials/options":                             true,
	"GET /api/modern/groups/:group_id/credentials":                    true,
	"GET /api/modern/groups/:group_id/credentials/:credential_id":     true,
	"GET /api/modern/groups/:group_id/credentials/:credential_id/rpm": true,
}

func guard(resetAt time.Time, web httproute.Module) gin.HandlerFunc {
	publicRoutes := map[string]bool{"GET /health": true}
	for _, route := range web.Routes {
		for _, method := range route.Methods {
			publicRoutes[method+" "+web.Prefix+route.Path] = true
		}
	}
	return func(c *gin.Context) {
		c.Header("X-GPT-Load-Demo-Reset-At", strconv.FormatInt(resetAt.UnixMilli(), 10))
		key := c.Request.Method + " " + c.FullPath()
		if !allowedRoutes[key] && !publicRoutes[key] {
			response.ErrorI18nFromAPIError(c, &app_errors.APIError{
				HTTPStatus: http.StatusForbidden, Code: "DEMO_RESTRICTED", Message: "demo.restricted",
			}, "demo.restricted")
			c.Abort()
			return
		}
		c.Next()
	}
}
