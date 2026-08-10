package control

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

const channelMaxQueryRunes = 200

type ChannelListResponse struct {
	Items []channel.Descriptor `json:"items"`
	Total int                  `json:"total"`
}

func (s *Service) ListChannels(ctx context.Context, query string) (ChannelListResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ChannelListResponse{}, err
	}
	if s == nil || s.channelRegistry == nil {
		return ChannelListResponse{}, app_errors.ErrInternalServer
	}
	items := s.channelRegistry.Search(query)
	return ChannelListResponse{Items: items, Total: len(items)}, nil
}

func parseChannelQuery(rawQuery string, forceQuery bool) (string, *app_errors.APIError) {
	if forceQuery && rawQuery == "" {
		return "", app_errors.ErrBadRequest
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", app_errors.ErrBadRequest
	}
	for key, entries := range values {
		if key != "q" || len(entries) != 1 {
			return "", app_errors.ErrBadRequest
		}
	}
	query := strings.TrimSpace(values.Get("q"))
	if utf8.RuneCountInString(query) > channelMaxQueryRunes {
		return "", app_errors.ErrBadRequest
	}
	return query, nil
}

func (s *Server) handleListChannels(c *gin.Context) {
	query, apiErr := parseChannelQuery(c.Request.URL.RawQuery, c.Request.URL.ForceQuery)
	if apiErr != nil {
		writeServiceError(c, "list_channels", apiErr)
		return
	}
	result, err := s.service.ListChannels(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_channels", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
