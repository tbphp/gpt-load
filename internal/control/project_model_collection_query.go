package control

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/platform/errors"
)

const (
	defaultProjectModelPage     int64 = 1
	defaultProjectModelPageSize int64 = 20
	maxProjectModelPageSize     int64 = 100
	maxProjectModelSearchRunes        = 200
)

type ProjectModelGroupStatus string

const (
	ProjectModelGroupStatusEnabled ProjectModelGroupStatus = "enabled"
	ProjectModelGroupStatusAll     ProjectModelGroupStatus = "all"
)

type ProjectModelPricingStatus string

const (
	ProjectModelPricingStatusPending    ProjectModelPricingStatus = "pending"
	ProjectModelPricingStatusConfigured ProjectModelPricingStatus = "configured"
	ProjectModelPricingStatusAll        ProjectModelPricingStatus = "all"
)

type ProjectModelListQuery struct {
	GroupStatus   ProjectModelGroupStatus
	PricingStatus ProjectModelPricingStatus
	Search        string
	Page          int64
	PageSize      int64
	AccessKeyID   *uint
}

func parseProjectModelListQuery(rawQuery string, forceQuery bool) (ProjectModelListQuery, *errors.APIError) {
	query := ProjectModelListQuery{
		GroupStatus:   ProjectModelGroupStatusEnabled,
		PricingStatus: ProjectModelPricingStatusAll,
		Page:          defaultProjectModelPage,
		PageSize:      defaultProjectModelPageSize,
	}
	if forceQuery && rawQuery == "" {
		return ProjectModelListQuery{}, errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ProjectModelListQuery{}, errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "group_status", "pricing_status", "q", "page", "page_size":
		default:
			return ProjectModelListQuery{}, errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return ProjectModelListQuery{}, errors.ErrBadRequest
		}
	}
	if entries, ok := values["group_status"]; ok {
		query.GroupStatus = ProjectModelGroupStatus(entries[0])
	}
	switch query.GroupStatus {
	case ProjectModelGroupStatusEnabled, ProjectModelGroupStatusAll:
	default:
		return ProjectModelListQuery{}, errors.ErrBadRequest
	}
	if entries, ok := values["pricing_status"]; ok {
		query.PricingStatus = ProjectModelPricingStatus(entries[0])
	}
	switch query.PricingStatus {
	case ProjectModelPricingStatusPending, ProjectModelPricingStatusConfigured, ProjectModelPricingStatusAll:
	default:
		return ProjectModelListQuery{}, errors.ErrBadRequest
	}
	if entries, ok := values["q"]; ok {
		query.Search = strings.TrimSpace(entries[0])
		if utf8.RuneCountInString(query.Search) > maxProjectModelSearchRunes {
			return ProjectModelListQuery{}, errors.ErrBadRequest
		}
	}
	if entries, ok := values["page"]; ok {
		page, parseErr := parseCanonicalSafeUint(entries[0])
		if parseErr != nil || page == 0 {
			return ProjectModelListQuery{}, errors.ErrBadRequest
		}
		query.Page = int64(page)
	}
	if entries, ok := values["page_size"]; ok {
		pageSize, parseErr := parseCanonicalSafeUint(entries[0])
		if parseErr != nil || pageSize == 0 || pageSize > uint64(maxProjectModelPageSize) {
			return ProjectModelListQuery{}, errors.ErrBadRequest
		}
		query.PageSize = int64(pageSize)
	}
	return query, nil
}

func validateProjectModelListQuery(query ProjectModelListQuery) error {
	switch query.GroupStatus {
	case ProjectModelGroupStatusEnabled, ProjectModelGroupStatusAll:
	default:
		return errors.ErrValidation
	}
	switch query.PricingStatus {
	case ProjectModelPricingStatusPending, ProjectModelPricingStatusConfigured, ProjectModelPricingStatusAll:
	default:
		return errors.ErrValidation
	}
	if query.Page <= 0 || query.Page > maxSafeInteger ||
		query.PageSize <= 0 || query.PageSize > maxProjectModelPageSize ||
		utf8.RuneCountInString(query.Search) > maxProjectModelSearchRunes {
		return errors.ErrValidation
	}
	if query.AccessKeyID != nil &&
		(*query.AccessKeyID == 0 || query.GroupStatus != ProjectModelGroupStatusEnabled) {
		return errors.ErrValidation
	}
	return nil
}
