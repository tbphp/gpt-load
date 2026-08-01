package control

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/state"
)

const (
	accessKeyCollectionMaxPageSize   int64 = 100
	accessKeyCollectionMaxQueryRunes       = 200
)

func (s *Server) handleListAccessKeyCollection(c *gin.Context) {
	query, apiErr := parseAccessKeyCollectionQuery(
		c.Request.URL.RawQuery,
		c.Request.URL.ForceQuery,
	)
	if apiErr != nil {
		writeServiceError(c, "list_access_keys", apiErr)
		return
	}
	result, err := s.service.ListAccessKeyCollection(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_access_keys", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseAccessKeyCollectionQuery(
	rawQuery string,
	forceQuery bool,
) (AccessKeyCollectionQuery, *app_errors.APIError) {
	query := AccessKeyCollectionQuery{
		Page:     defaultAccessKeyCollectionPage,
		PageSize: defaultAccessKeyCollectionPageSize,
	}
	if forceQuery && rawQuery == "" {
		return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "q", "status", "page", "page_size":
		default:
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
	}

	if entries, exists := values["q"]; exists {
		query.Query = strings.TrimSpace(entries[0])
		if utf8.RuneCountInString(query.Query) > accessKeyCollectionMaxQueryRunes {
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["status"]; exists {
		status, ok := parseAccessKeyCollectionStatus(entries[0])
		if !ok {
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Status = &status
	}
	if entries, exists := values["page"]; exists {
		page, ok := parseAccessKeyCollectionPositiveInt(entries[0])
		if !ok {
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Page = page
	}
	if entries, exists := values["page_size"]; exists {
		pageSize, ok := parseAccessKeyCollectionPositiveInt(entries[0])
		if !ok || pageSize > accessKeyCollectionMaxPageSize {
			return AccessKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = pageSize
	}
	return query, nil
}

func parseAccessKeyCollectionStatus(value string) (state.AccessKeyStatus, bool) {
	status := state.AccessKeyStatus(value)
	switch status {
	case state.AccessKeyStatusActive, state.AccessKeyStatusDisabled:
		return status, true
	default:
		return "", false
	}
}

func parseAccessKeyCollectionPositiveInt(value string) (int64, bool) {
	if value == "" || value[0] == '0' {
		return 0, false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}
