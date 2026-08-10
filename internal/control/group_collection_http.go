package control

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

const (
	groupCollectionDefaultPage     int64 = 1
	groupCollectionDefaultPageSize int64 = 20
	groupCollectionMaxPageSize     int64 = 100
	groupCollectionMaxQueryRunes         = 200
)

func (s *Server) handleListGroupCollection(c *gin.Context) {
	query, apiErr := parseGroupCollectionQuery(
		c.Request.URL.RawQuery,
		c.Request.URL.ForceQuery,
	)
	if apiErr != nil {
		writeServiceError(c, "list_groups", apiErr)
		return
	}
	result, err := s.service.ListGroupCollection(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_groups", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleListGroupOptions(c *gin.Context) {
	if c.Request.URL.ForceQuery || c.Request.URL.RawQuery != "" {
		writeServiceError(c, "list_group_options", app_errors.ErrBadRequest)
		return
	}
	result, err := s.service.ListGroupOptions(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_group_options", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseGroupCollectionQuery(
	rawQuery string,
	forceQuery bool,
) (GroupCollectionQuery, *app_errors.APIError) {
	query := GroupCollectionQuery{
		Sort:     GroupCollectionSortStatus,
		Page:     groupCollectionDefaultPage,
		PageSize: groupCollectionDefaultPageSize,
	}
	if forceQuery && rawQuery == "" {
		return GroupCollectionQuery{}, app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return GroupCollectionQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "q", "status", "sort", "page", "page_size":
		default:
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
	}

	if entries, exists := values["q"]; exists {
		query.Query = strings.TrimSpace(entries[0])
		if utf8.RuneCountInString(query.Query) > groupCollectionMaxQueryRunes {
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["status"]; exists {
		status, ok := parseGroupCollectionStatus(entries[0])
		if !ok {
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Status = status
	}
	if entries, exists := values["sort"]; exists {
		sortValue := GroupCollectionSort(entries[0])
		switch sortValue {
		case GroupCollectionSortStatus,
			GroupCollectionSortName,
			GroupCollectionSortCredentials,
			GroupCollectionSortCreated:
			query.Sort = sortValue
		default:
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["page"]; exists {
		page, ok := parseGroupCollectionPositiveInt(entries[0])
		if !ok {
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Page = page
	}
	if entries, exists := values["page_size"]; exists {
		pageSize, ok := parseGroupCollectionPositiveInt(entries[0])
		if !ok || pageSize > groupCollectionMaxPageSize {
			return GroupCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = pageSize
	}
	return query, nil
}

func parseGroupCollectionStatus(
	value string,
) (*GroupCollectionStatus, bool) {
	status := GroupCollectionStatus(value)
	switch status {
	case GroupCollectionStatusAvailable,
		GroupCollectionStatusUnavailable,
		GroupCollectionStatusDisabled:
		return &status, true
	default:
		return nil, false
	}
}

func parseGroupCollectionPositiveInt(value string) (int64, bool) {
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
