package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

const (
	credentialBalanceTimeout = 20 * time.Second
	credentialBalanceMaxBody = 64 << 10
	// credentialBalanceConcurrency bounds simultaneous upstream balance calls so
	// a large group cannot open hundreds of sockets at once.
	credentialBalanceConcurrency = 4
)

// balanceKind identifies an upstream whose account balance can be read from a
// public endpoint. Providers without one are reported as unsupported rather
// than failing, so the console can hide the value instead of showing an error.
type balanceKind string

const (
	balanceKindDeepSeek balanceKind = "deepseek"
	balanceKindMoonshot balanceKind = "moonshot"
)

var balanceDefaultBaseURLs = map[balanceKind]string{
	balanceKindDeepSeek: "https://api.deepseek.com",
	balanceKindMoonshot: "https://api.moonshot.cn/v1",
}

// balanceEndpointPaths are appended to the resolved base URL.
var balanceEndpointPaths = map[balanceKind]string{
	balanceKindDeepSeek: "/user/balance",
	balanceKindMoonshot: "/users/me/balance",
}

func balanceKindForChannel(id channel.ID) (balanceKind, bool) {
	switch id {
	case channel.DeepSeek:
		return balanceKindDeepSeek, true
	case channel.MoonshotAI:
		return balanceKindMoonshot, true
	default:
		return "", false
	}
}

// CredentialBalanceEntry is one currency line of an upstream account balance.
type CredentialBalanceEntry struct {
	Currency string `json:"currency"`
	Total    string `json:"total_balance"`
	Granted  string `json:"granted_balance,omitempty"`
	ToppedUp string `json:"topped_up_balance,omitempty"`
}

// CredentialBalanceResponse reports the upstream balance of a single credential.
type CredentialBalanceResponse struct {
	GroupID      uint                     `json:"group_id"`
	CredentialID uint                     `json:"credential_id"`
	ChannelID    string                   `json:"channel_id"`
	Supported    bool                     `json:"supported"`
	Available    bool                     `json:"available"`
	Message      string                   `json:"message,omitempty"`
	Balances     []CredentialBalanceEntry `json:"balances"`
}

// GroupCredentialBalancesResponse reports balances for every active credential
// in a group, so the console can render them inline without one request per row.
type GroupCredentialBalancesResponse struct {
	GroupID   uint                        `json:"group_id"`
	ChannelID string                      `json:"channel_id"`
	Supported bool                        `json:"supported"`
	Message   string                      `json:"message,omitempty"`
	Items     []CredentialBalanceResponse `json:"items"`
}

type credentialBalanceRows struct {
	group       models.Group
	credentials []models.Credential
}

// balanceTarget is the resolved upstream endpoint for a balance-capable group.
type balanceTarget struct {
	kind    balanceKind
	baseURL string
}

// balanceUpstreamRejection reports that the upstream answered but declined the
// credential query, distinguishing it from a transport-level failure. It is
// reported in-band and must never surface as HTTP 401: the console treats 401
// on a management call as an expired session and signs the operator out.
type balanceUpstreamRejection struct {
	Status int
	Reason string
}

func (e *balanceUpstreamRejection) Error() string {
	return fmt.Sprintf("upstream rejected balance query with status %d", e.Status)
}

// QueryCredentialBalance reads the upstream account balance for one credential.
// Cached values are reused unless refresh is set.
func (s *Service) QueryCredentialBalance(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	refresh bool,
) (CredentialBalanceResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialBalanceResponse{}, app_errors.ErrBadRequest
	}
	if !s.balanceDependenciesReady() {
		return CredentialBalanceResponse{}, app_errors.ErrInternalServer
	}

	if !refresh {
		if entry, ok := s.balances.fresh(credentialID); ok {
			return entry.response, nil
		}
	}

	rows, err := s.readCredentialBalanceRows(ctx, groupID, []uint{credentialID})
	if err != nil {
		return CredentialBalanceResponse{}, err
	}
	if len(rows.credentials) == 0 {
		return CredentialBalanceResponse{}, app_errors.ErrResourceNotFound
	}

	response := newCredentialBalanceResponse(groupID, rows.credentials[0].ID, rows.group)
	target, ok, err := s.resolveBalanceTarget(rows.group)
	if err != nil {
		return CredentialBalanceResponse{}, err
	}
	if !ok {
		return response, nil
	}

	response = s.balanceForCredential(ctx, rows.group, rows.credentials[0], target, response)
	return response, nil
}

// QueryGroupCredentialBalances reads balances for every active credential in a
// group concurrently. Individual upstream failures are reported per item so one
// bad credential cannot blank out the whole list.
func (s *Service) QueryGroupCredentialBalances(
	ctx context.Context,
	groupID uint,
	refresh bool,
) (GroupCredentialBalancesResponse, error) {
	if groupID == 0 {
		return GroupCredentialBalancesResponse{}, app_errors.ErrBadRequest
	}
	if !s.balanceDependenciesReady() {
		return GroupCredentialBalancesResponse{}, app_errors.ErrInternalServer
	}

	rows, err := s.readCredentialBalanceRows(ctx, groupID, nil)
	if err != nil {
		return GroupCredentialBalancesResponse{}, err
	}

	channelID := channel.ID(strings.TrimSpace(rows.group.ChannelID))
	result := GroupCredentialBalancesResponse{
		GroupID:   groupID,
		ChannelID: string(channelID),
		Items:     make([]CredentialBalanceResponse, 0, len(rows.credentials)),
	}

	target, ok, err := s.resolveBalanceTarget(rows.group)
	if err != nil {
		return GroupCredentialBalancesResponse{}, err
	}
	if !ok {
		result.Message = "此渠道不支持余额查询"
		return result, nil
	}
	result.Supported = true

	items := make([]CredentialBalanceResponse, len(rows.credentials))
	semaphore := make(chan struct{}, credentialBalanceConcurrency)
	var waitGroup sync.WaitGroup
	for index := range rows.credentials {
		credential := rows.credentials[index]
		item := newCredentialBalanceResponse(groupID, credential.ID, rows.group)
		if !refresh {
			if entry, cached := s.balances.fresh(credential.ID); cached {
				items[index] = entry.response
				continue
			}
		}
		items[index] = item
		waitGroup.Add(1)
		go func(slot int, row models.Credential) {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			items[slot] = s.balanceForCredential(ctx, rows.group, row, target, items[slot])
		}(index, credential)
	}
	waitGroup.Wait()

	if parentErr := ctx.Err(); parentErr != nil {
		return GroupCredentialBalancesResponse{}, parentErr
	}
	result.Items = items
	return result, nil
}

// GroupBalanceTotal is the aggregate balance of one group, used by the group
// list so it can render a total without one upstream call per key.
type GroupBalanceTotal struct {
	GroupID   uint                     `json:"group_id"`
	ChannelID string                   `json:"channel_id"`
	Supported bool                     `json:"supported"`
	Available bool                     `json:"available"`
	Known     bool                     `json:"known"`
	Totals    []CredentialBalanceEntry `json:"totals"`
	Message   string                   `json:"message,omitempty"`
}

// GroupBalanceTotalsResponse wraps per-group totals for the group list view.
type GroupBalanceTotalsResponse struct {
	Items []GroupBalanceTotal `json:"items"`
}

// QueryGroupBalanceTotals aggregates cached balances per group. Only groups
// whose channel supports balance queries are reported.
func (s *Service) QueryGroupBalanceTotals(
	ctx context.Context,
	refresh bool,
) ([]GroupBalanceTotal, error) {
	if !s.balanceDependenciesReady() {
		return nil, app_errors.ErrInternalServer
	}
	groups, err := s.readAllBalanceGroups(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]GroupBalanceTotal, 0, len(groups))
	for _, group := range groups {
		channelID := channel.ID(strings.TrimSpace(group.ChannelID))
		if _, supported := balanceKindForChannel(channelID); !supported {
			continue
		}
		total := GroupBalanceTotal{
			GroupID:   group.ID,
			ChannelID: string(channelID),
			Supported: true,
			Totals:    []CredentialBalanceEntry{},
		}

		rows, rowsErr := s.readCredentialBalanceRows(ctx, group.ID, nil)
		if rowsErr != nil {
			return nil, rowsErr
		}
		target, ok, targetErr := s.resolveBalanceTarget(group)
		if targetErr != nil {
			return nil, targetErr
		}
		if !ok {
			continue
		}

		entries := make([]CredentialBalanceEntry, 0, len(rows.credentials))
		known := false
		for _, credential := range rows.credentials {
			var response CredentialBalanceResponse
			if entry, cached := s.balances.fresh(credential.ID); cached && !refresh {
				response = entry.response
			} else {
				response = s.balanceForCredential(
					ctx, rows.group, credential, target,
					newCredentialBalanceResponse(group.ID, credential.ID, rows.group),
				)
			}
			if !response.Available {
				continue
			}
			known = true
			entries = append(entries, response.Balances...)
		}
		if known {
			total.Known = true
			total.Available = true
			total.Totals = sumBalanceEntries(entries)
		}
		results = append(results, total)
	}
	return results, nil
}

// readAllBalanceGroups loads every group that could carry a balance.
func (s *Service) readAllBalanceGroups(ctx context.Context) ([]models.Group, error) {
	var groups []models.Group
	loadErr := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		loaded := make([]models.Group, 0, 16)
		if err := tx.Order("id ASC").Find(&loaded).Error; err != nil {
			return err
		}
		groups = loaded
		return nil
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return nil, parentErr
	}
	if loadErr != nil {
		return nil, loadErr
	}
	return groups, nil
}

// balanceForCredential fetches one credential's balance, caching the outcome so
// repeated page views reuse it. Failures that are not upstream rejections are
// reported in-band for batch callers rather than aborting the whole group.
func (s *Service) balanceForCredential(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
	target balanceTarget,
	response CredentialBalanceResponse,
) CredentialBalanceResponse {
	balances, rejection, err := s.fetchCredentialBalanceValue(ctx, group, credential, target)
	var applied CredentialBalanceResponse
	switch {
	case err != nil:
		applied = s.applyBalance(response, nil, &balanceUpstreamRejection{
			Status: 0,
			Reason: err.Error(),
		})
	case rejection != nil:
		applied = s.applyBalance(response, nil, rejection)
	default:
		applied = s.applyBalance(response, balances, nil)
	}
	s.balances.put(credential.ID, applied)
	return applied
}

func (s *Service) balanceDependenciesReady() bool {
	return s != nil &&
		s.db != nil &&
		s.channelRegistry != nil &&
		s.encryption != nil &&
		s.httpClient != nil
}

func newCredentialBalanceResponse(
	groupID uint,
	credentialID uint,
	group models.Group,
) CredentialBalanceResponse {
	return CredentialBalanceResponse{
		GroupID:      groupID,
		CredentialID: credentialID,
		ChannelID:    strings.TrimSpace(group.ChannelID),
		Balances:     []CredentialBalanceEntry{},
	}
}

// resolveBalanceTarget reports whether the group's channel has a public balance
// endpoint and, if so, where it lives.
func (s *Service) resolveBalanceTarget(group models.Group) (balanceTarget, bool, error) {
	channelID := channel.ID(strings.TrimSpace(group.ChannelID))
	kind, supported := balanceKindForChannel(channelID)
	if !supported {
		return balanceTarget{}, false, nil
	}
	if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeAPIKey {
		return balanceTarget{}, false, nil
	}
	resolved, err := s.channelRegistry.Resolve(channelID, json.RawMessage(group.Params))
	if err != nil {
		return balanceTarget{}, false,
			fmt.Errorf("resolve balance channel target: %w", app_errors.ErrInternalServer)
	}
	baseURL, err := balanceBaseURL(kind, resolved.TargetConfig)
	if err != nil {
		return balanceTarget{}, false, err
	}
	return balanceTarget{kind: kind, baseURL: baseURL}, true, nil
}

func (s *Service) applyBalance(
	response CredentialBalanceResponse,
	balances []CredentialBalanceEntry,
	rejection *balanceUpstreamRejection,
) CredentialBalanceResponse {
	response.Supported = true
	if rejection != nil {
		response.Available = false
		response.Message = rejection.Reason
		return response
	}
	response.Available = true
	response.Message = ""
	if balances == nil {
		balances = []CredentialBalanceEntry{}
	}
	response.Balances = balances
	return response
}

// fetchCredentialBalanceValue decrypts the credential and queries its upstream
// balance. A non-nil rejection means the upstream answered but refused.
func (s *Service) fetchCredentialBalanceValue(
	ctx context.Context,
	group models.Group,
	credential models.Credential,
	target balanceTarget,
) ([]CredentialBalanceEntry, *balanceUpstreamRejection, error) {
	_, apiKey, err := s.decodeCredential(group, credential)
	if err != nil {
		return nil, nil, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, nil, app_errors.ErrValidation
	}

	balances, err := s.fetchCredentialBalance(ctx, target.kind, target.baseURL, apiKey)
	if err != nil {
		var rejection *balanceUpstreamRejection
		if errors.As(err, &rejection) {
			return nil, rejection, nil
		}
		return nil, nil, err
	}
	return balances, nil, nil
}

// readCredentialBalanceRows loads the group plus its active credentials. When
// credentialIDs is empty every active credential is returned.
func (s *Service) readCredentialBalanceRows(
	ctx context.Context,
	groupID uint,
	credentialIDs []uint,
) (credentialBalanceRows, error) {
	var rows credentialBalanceRows
	loadErr := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		groups := make([]models.Group, 0, 1)
		if err := tx.Where("id = ?", groupID).Limit(1).Find(&groups).Error; err != nil {
			return err
		}
		if len(groups) == 0 {
			return app_errors.ErrResourceNotFound
		}
		rows.group = groups[0]

		query := tx.Where("group_id = ? AND status = ?", groupID, models.CredentialStatusActive)
		if len(credentialIDs) > 0 {
			query = query.Where("id IN ?", credentialIDs)
		}
		credentials := make([]models.Credential, 0, 8)
		if err := query.Order("id ASC").Find(&credentials).Error; err != nil {
			return err
		}
		rows.credentials = credentials
		return nil
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return credentialBalanceRows{}, parentErr
	}
	if loadErr != nil {
		return credentialBalanceRows{}, loadErr
	}
	return rows, nil
}

// balanceBaseURL prefers the group's configured base_url and falls back to the
// provider default when the group left it empty.
func balanceBaseURL(kind balanceKind, targetConfig json.RawMessage) (string, error) {
	candidate := ""
	if len(targetConfig) > 0 {
		var decoded struct {
			BaseURL string `json:"base_url"`
		}
		if err := json.Unmarshal(targetConfig, &decoded); err == nil {
			candidate = strings.TrimSpace(decoded.BaseURL)
		}
	}
	if candidate == "" {
		candidate = balanceDefaultBaseURLs[kind]
	}
	if candidate == "" {
		return "", fmt.Errorf("no base url for balance channel: %w", app_errors.ErrInternalServer)
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid balance base url: %w", app_errors.ErrValidation)
	}
	return strings.TrimRight(candidate, "/"), nil
}

func (s *Service) fetchCredentialBalance(
	ctx context.Context,
	kind balanceKind,
	baseURL string,
	apiKey string,
) ([]CredentialBalanceEntry, error) {
	if s.httpClient == nil {
		return nil, fmt.Errorf("balance http client unavailable: %w", app_errors.ErrInternalServer)
	}
	endpoint := baseURL + balanceEndpointPaths[kind]

	requestCtx, cancel := context.WithTimeout(ctx, credentialBalanceTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build balance request: %w", app_errors.ErrInternalServer)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")

	httpResponse, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query upstream balance: %w", app_errors.ErrBadGateway)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, credentialBalanceMaxBody))
		_ = httpResponse.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, credentialBalanceMaxBody))
	if err != nil {
		return nil, fmt.Errorf("read upstream balance: %w", app_errors.ErrBadGateway)
	}
	if httpResponse.StatusCode != http.StatusOK {
		switch httpResponse.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, &balanceUpstreamRejection{
				Status: httpResponse.StatusCode,
				Reason: "The upstream rejected this credential.",
			}
		case http.StatusTooManyRequests:
			return nil, &balanceUpstreamRejection{
				Status: httpResponse.StatusCode,
				Reason: "The upstream rate limited the balance query.",
			}
		default:
			return nil, fmt.Errorf(
				"upstream balance status %d: %w",
				httpResponse.StatusCode,
				app_errors.ErrBadGateway,
			)
		}
	}

	switch kind {
	case balanceKindDeepSeek:
		return parseDeepSeekBalance(body)
	case balanceKindMoonshot:
		return parseMoonshotBalance(body)
	default:
		return nil, fmt.Errorf("unknown balance provider: %w", app_errors.ErrInternalServer)
	}
}

func parseDeepSeekBalance(body []byte) ([]CredentialBalanceEntry, error) {
	var payload struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode deepseek balance: %w", app_errors.ErrInternalServer)
	}
	entries := make([]CredentialBalanceEntry, 0, len(payload.BalanceInfos))
	for _, info := range payload.BalanceInfos {
		entries = append(entries, CredentialBalanceEntry{
			Currency: info.Currency,
			Total:    info.TotalBalance,
			Granted:  info.GrantedBalance,
			ToppedUp: info.ToppedUpBalance,
		})
	}
	return entries, nil
}

func parseMoonshotBalance(body []byte) ([]CredentialBalanceEntry, error) {
	var payload struct {
		Data struct {
			AvailableBalance json.Number `json:"available_balance"`
			VoucherBalance   json.Number `json:"voucher_balance"`
			CashBalance      json.Number `json:"cash_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode moonshot balance: %w", app_errors.ErrInternalServer)
	}
	return []CredentialBalanceEntry{{
		Currency: "CNY",
		Total:    payload.Data.AvailableBalance.String(),
		Granted:  payload.Data.VoucherBalance.String(),
		ToppedUp: payload.Data.CashBalance.String(),
	}}, nil
}
