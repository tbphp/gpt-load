package control

import (
	"strconv"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

func proxyID(c *gin.Context) (uint, error) {
	id, err := parseCanonicalSafePlatformUint(c.Param("id"))
	if err != nil || id == 0 {
		return 0, app_errors.ErrBadRequest
	}
	return id, nil
}

func (s *Server) handleListProxies(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	query := ProxyListQuery{Search: c.Query("q"), State: c.Query("state"), Scheme: c.Query("scheme"), Used: c.Query("used"), Test: c.Query("test"), Sort: c.Query("sort"), Page: page, PageSize: size}
	if c.Query("all") == "true" {
		query.PageSize = -1
	}
	result, err := s.service.ListProxies(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_proxies", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleSaveProxy(c *gin.Context) {
	var request ProxySaveRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "save_proxy", mapControlJSONError(err))
		return
	}
	var id uint
	if c.Param("id") != "" {
		var err error
		id, err = proxyID(c)
		if err != nil {
			writeServiceError(c, "save_proxy", err)
			return
		}
	}
	result, err := s.service.SaveProxy(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "save_proxy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleProxyImpact(c *gin.Context) {
	var request struct {
		IDs []uint `json:"ids"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "proxy_impact", mapControlJSONError(err))
		return
	}
	if len(request.IDs) > 10000 {
		writeServiceError(c, "proxy_impact", app_errors.ErrValidation)
		return
	}
	result, err := s.service.ProxyImpact(c.Request.Context(), request.IDs)
	if err != nil {
		writeServiceError(c, "proxy_impact", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleBatchProxies(c *gin.Context) {
	var request ProxyBatchRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "batch_proxies", mapControlJSONError(err))
		return
	}
	if err := s.service.BatchProxies(c.Request.Context(), request); err != nil {
		writeServiceError(c, "batch_proxies", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}

func (s *Server) handleImportProxies(c *gin.Context) {
	var request struct {
		Entries string `json:"entries"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "import_proxies", mapControlJSONError(err))
		return
	}
	result, err := s.service.ImportProxies(c.Request.Context(), request.Entries)
	if err != nil {
		writeServiceError(c, "import_proxies", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleTestProxy(c *gin.Context) {
	id, err := proxyID(c)
	if err != nil {
		writeServiceError(c, "test_proxy", err)
		return
	}
	var request struct {
		URL string `json:"url"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "test_proxy", mapControlJSONError(err))
		return
	}
	result, err := s.service.TestProxy(c.Request.Context(), id, request.URL)
	if err != nil {
		writeServiceError(c, "test_proxy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleProxyTestURL(c *gin.Context) {
	var request struct {
		URL string `json:"url"`
	}
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "proxy_test_url", mapControlJSONError(err))
		return
	}
	if err := s.service.SaveProxyTestURL(c.Request.Context(), request.URL); err != nil {
		writeServiceError(c, "proxy_test_url", err)
		return
	}
	response.SuccessI18n(c, "common.success", nil)
}
