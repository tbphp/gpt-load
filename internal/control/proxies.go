package control

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/outboundproxy"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const proxyTestURLSetting = models.InternalSystemSettingPrefix + "proxy_test_url"
const defaultProxyTestURL = "https://www.gstatic.com/generate_204"

type ProxyReference struct {
	GroupID        uint   `json:"group_id"`
	GroupName      string `json:"group_name"`
	CredentialID   uint   `json:"credential_id,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"`
	Label          string `json:"label,omitempty"`
}

type ProxyReferences struct {
	Groups      []ProxyReference `json:"groups"`
	Credentials []ProxyReference `json:"credentials"`
	Global      bool             `json:"global"`
}

type ProxyItem struct {
	ID                 uint   `json:"id"`
	Name               string `json:"name"`
	DisplayURL         string `json:"display_url"`
	Scheme             string `json:"scheme"`
	HasAuth            bool   `json:"has_auth"`
	Enabled            bool   `json:"enabled"`
	GroupCount         int    `json:"group_count"`
	CredentialCount    int    `json:"credential_count"`
	Global             bool   `json:"global"`
	LastTestURL        string `json:"last_test_url"`
	LastTestAtMS       *int64 `json:"last_test_at_ms"`
	LastTestDurationMS *int64 `json:"last_test_duration_ms"`
	LastTestStatusCode *int   `json:"last_test_status_code"`
	LastTestError      string `json:"last_test_error"`
}

type ProxyListQuery struct {
	Search, State, Scheme, Used, Test, Sort string
	Page, PageSize                          int
}
type ProxyList struct {
	Items    []ProxyItem `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	TestURL  string      `json:"test_url"`
}
type ProxySaveRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type ProxyBatchRequest struct {
	IDs    []uint `json:"ids"`
	Action string `json:"action"`
}
type ProxyImportResult struct {
	Imported     int   `json:"imported"`
	Duplicates   int   `json:"duplicates"`
	InvalidLines []int `json:"invalid_lines"`
}

func (s *Service) proxyReferences(ctx context.Context, db *gorm.DB, labels ...bool) (map[uint]ProxyReferences, error) {
	result := make(map[uint]ProxyReferences)
	add := func(ciphertext *string, reference ProxyReference, global bool) error {
		config, err := decryptProxyOverride(s.encryption, ciphertext)
		if err != nil {
			return err
		}
		if config == nil || config.ProxyID == 0 {
			return nil
		}
		value := result[config.ProxyID]
		if value.Groups == nil {
			value.Groups = []ProxyReference{}
			value.Credentials = []ProxyReference{}
		}
		if global {
			value.Global = true
		} else if reference.CredentialID != 0 {
			value.Credentials = append(value.Credentials, reference)
		} else {
			value.Groups = append(value.Groups, reference)
		}
		result[config.ProxyID] = value
		return nil
	}
	var settings []models.SystemSetting
	if err := globalProxyConfigScope(db.WithContext(ctx)).Find(&settings).Error; err != nil {
		return nil, proxyDatabaseError(err)
	}
	for _, row := range settings {
		if err := add(&row.Value, ProxyReference{}, true); err != nil {
			return nil, err
		}
	}
	var groups []models.Group
	if err := db.WithContext(ctx).Order("id ASC").Find(&groups).Error; err != nil {
		return nil, proxyDatabaseError(err)
	}
	byID := make(map[uint]models.Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
		if err := add(group.ProxyConfig, ProxyReference{GroupID: group.ID, GroupName: group.Name}, false); err != nil {
			return nil, err
		}
	}
	var credentials []models.Credential
	if err := db.WithContext(ctx).Where("proxy_config IS NOT NULL").Order("id ASC").Find(&credentials).Error; err != nil {
		return nil, proxyDatabaseError(err)
	}
	for _, credential := range credentials {
		group := byID[credential.GroupID]
		label := ""
		if len(labels) > 0 && labels[0] {
			canonical, identity, err := s.decodeCredential(group, credential)
			if err != nil {
				return nil, err
			}
			mask, account, err := s.credentialPresentation(group, credential, canonical, identity)
			if err != nil {
				return nil, err
			}
			if group.ConnectionType == models.ConnectionTypeSubscription {
				label = account.EmailMask
			} else {
				label = mask
			}
		}
		if err := add(credential.ProxyConfig, ProxyReference{GroupID: group.ID, GroupName: group.Name, CredentialID: credential.ID, ConnectionType: string(group.ConnectionType), Label: label}, false); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func proxyItem(value stateloader.ManagedProxy, refs ProxyReferences) (ProxyItem, error) {
	display, auth, err := outboundproxy.Display(value.Config)
	if err != nil {
		return ProxyItem{}, app_errors.ErrInternalServer
	}
	endpoint, err := url.Parse(value.Config.URL)
	if err != nil {
		return ProxyItem{}, app_errors.ErrInternalServer
	}
	row := value.Row
	return ProxyItem{ID: row.ID, Name: row.Name, DisplayURL: display, Scheme: endpoint.Scheme, HasAuth: auth, Enabled: row.Enabled,
		GroupCount: len(refs.Groups), CredentialCount: len(refs.Credentials), Global: refs.Global,
		LastTestURL: row.LastTestURL, LastTestAtMS: row.LastTestAtMS, LastTestDurationMS: row.LastTestDurationMS, LastTestStatusCode: row.LastTestStatusCode, LastTestError: row.LastTestError}, nil
}

func (s *Service) ListProxies(ctx context.Context, query ProxyListQuery) (ProxyList, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	result := ProxyList{Items: []ProxyItem{}, Page: max(1, query.Page), PageSize: query.PageSize}
	if result.PageSize < 1 || result.PageSize > 100 {
		result.PageSize = 20
	}
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		catalog, err := stateloader.LoadProxyCatalog(ctx, tx, s.encryption)
		if err != nil {
			return app_errors.ErrInternalServer
		}
		references, err := s.proxyReferences(ctx, tx)
		if err != nil {
			return err
		}
		for _, proxy := range catalog {
			item, err := proxyItem(proxy, references[proxy.Row.ID])
			if err != nil {
				return err
			}
			if matchProxy(item, query) {
				result.Items = append(result.Items, item)
			}
		}
		result.TestURL, err = loadProxyTestURL(ctx, tx)
		return err
	})
	if err != nil {
		return ProxyList{}, err
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		if query.Sort == "latency" {
			if (left.LastTestDurationMS == nil) != (right.LastTestDurationMS == nil) {
				return left.LastTestDurationMS != nil
			}
			if left.LastTestDurationMS != nil && right.LastTestDurationMS != nil && *left.LastTestDurationMS != *right.LastTestDurationMS {
				return *left.LastTestDurationMS < *right.LastTestDurationMS
			}
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.ID < right.ID
	})
	result.Total = len(result.Items)
	if query.PageSize != -1 {
		start := result.Total
		if result.Page-1 <= result.Total/result.PageSize {
			start = (result.Page - 1) * result.PageSize
		}
		result.Items = result.Items[start:min(start+result.PageSize, result.Total)]
	}
	return result, nil
}

func matchProxy(item ProxyItem, query ProxyListQuery) bool {
	if query.Search != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.DisplayURL), strings.ToLower(strings.TrimSpace(query.Search))) {
		return false
	}
	if query.State == "enabled" && !item.Enabled || query.State == "disabled" && item.Enabled {
		return false
	}
	if query.Scheme != "" && item.Scheme != query.Scheme {
		return false
	}
	used := item.Global || item.GroupCount+item.CredentialCount > 0
	if query.Used == "used" && !used || query.Used == "unused" && used {
		return false
	}
	success := item.LastTestError == "" && item.LastTestStatusCode != nil && *item.LastTestStatusCode >= 200 && *item.LastTestStatusCode < 300
	if query.Test == "untested" && item.LastTestAtMS != nil || query.Test == "success" && !success || query.Test == "failed" && (item.LastTestAtMS == nil || success) {
		return false
	}
	return true
}

func (s *Service) ProxyImpact(ctx context.Context, ids []uint) (ProxyReferences, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	result := ProxyReferences{Groups: []ProxyReference{}, Credentials: []ProxyReference{}}
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		refs, err := s.proxyReferences(ctx, tx, true)
		if err != nil {
			return err
		}
		seen := make(map[uint]bool)
		for _, id := range ids {
			if !seen[id] {
				value := refs[id]
				result.Groups = append(result.Groups, value.Groups...)
				result.Credentials = append(result.Credentials, value.Credentials...)
				result.Global = result.Global || value.Global
				seen[id] = true
			}
		}
		return nil
	})
	return result, err
}

// writeProxies 沿用既有配置发布和凭据协调，避免只更新快照却遗漏凭据级覆盖。
func (s *Service) writeProxies(ctx context.Context, mutate func(*gorm.DB) error) error {
	var entries []state.CredentialEntry
	_, err := s.writeGroupConfig(ctx, func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		var err error
		entries, err = stateloader.BuildCredentialEntriesWithProxy(ctx, tx, s.encryption)
		return err
	}, func() error {
		groups := make(map[uint][]state.CredentialEntry)
		var retired []uint
		for _, entry := range entries {
			if previous, exists := s.registry.CredentialRef(entry.ID); exists && previous.ProxyFingerprint != entry.ProxyFingerprint {
				retired = append(retired, entry.ID)
			}
			groups[entry.GroupID] = append(groups[entry.GroupID], entry)
		}
		for groupID, rows := range groups {
			if _, err := s.reconcileRegistryGroup(groupID, rows); err != nil {
				return err
			}
		}
		for _, id := range retired {
			s.retireCredentialRuntime(id)
		}
		return nil
	})
	return err
}

func (s *Service) SaveProxy(ctx context.Context, id uint, request ProxySaveRequest) (ProxyItem, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return ProxyItem{}, app_errors.ErrValidation
	}
	var saved models.Proxy
	err := s.writeProxies(ctx, func(tx *gorm.DB) error {
		if id != 0 {
			if err := tx.Take(&saved, id).Error; err != nil {
				return proxyDatabaseError(err)
			}
		}
		if id == 0 || strings.TrimSpace(request.URL) != "" {
			config, err := outboundproxy.CanonicalConnection(outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: request.URL})
			if err != nil {
				return app_errors.ErrValidation
			}
			encoded, err := outboundproxy.Encode(config)
			if err != nil {
				return app_errors.ErrValidation
			}
			saved.Fingerprint = s.encryption.Hash(encoded)
			var count int64
			if err := tx.Model(&models.Proxy{}).Where("fingerprint = ? AND id <> ?", saved.Fingerprint, id).Count(&count).Error; err != nil {
				return proxyDatabaseError(err)
			}
			if count != 0 {
				return app_errors.ErrDuplicateResource
			}
			saved.Config, err = s.encryption.Encrypt(encoded)
			if err != nil {
				return app_errors.ErrInternalServer
			}
		}
		saved.Name = name
		if id == 0 {
			saved.Enabled = true
			return proxyDatabaseError(tx.Create(&saved).Error)
		}
		return proxyDatabaseError(tx.Model(&models.Proxy{}).Where("id = ?", id).Updates(map[string]any{"name": saved.Name, "config": saved.Config, "fingerprint": saved.Fingerprint}).Error)
	})
	if err != nil {
		return ProxyItem{}, err
	}
	list, err := s.ListProxies(ctx, ProxyListQuery{PageSize: -1})
	if err != nil {
		return ProxyItem{}, err
	}
	for _, item := range list.Items {
		if item.ID == saved.ID {
			return item, nil
		}
	}
	return ProxyItem{}, app_errors.ErrResourceNotFound
}

func (s *Service) BatchProxies(ctx context.Context, request ProxyBatchRequest) error {
	if len(request.IDs) == 0 || len(request.IDs) > 10000 {
		return app_errors.ErrValidation
	}
	if request.Action != "enable" && request.Action != "disable" && request.Action != "delete" {
		return app_errors.ErrValidation
	}
	return s.writeProxies(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&models.Proxy{}).Where("id IN ?", request.IDs)
		if request.Action == "delete" {
			return proxyDatabaseError(query.Delete(&models.Proxy{}).Error)
		}
		return proxyDatabaseError(query.Update("enabled", request.Action == "enable").Error)
	})
}

func (s *Service) ImportProxies(ctx context.Context, text string) (ProxyImportResult, error) {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	result := ProxyImportResult{InvalidLines: []int{}}
	if len(lines) > 1000 {
		return result, app_errors.ErrValidation
	}
	err := s.writeProxies(ctx, func(tx *gorm.DB) error {
		catalog, err := stateloader.LoadProxyCatalog(ctx, tx, s.encryption)
		if err != nil {
			return app_errors.ErrInternalServer
		}
		fingerprints := make(map[string]bool, len(catalog))
		for _, proxy := range catalog {
			fingerprints[proxy.Row.Fingerprint] = true
		}
		for index, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			config, err := outboundproxy.CanonicalConnection(outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: line})
			if err != nil {
				result.InvalidLines = append(result.InvalidLines, index+1)
				continue
			}
			encoded, err := outboundproxy.Encode(config)
			if err != nil {
				return app_errors.ErrValidation
			}
			fingerprint := s.encryption.Hash(encoded)
			if fingerprints[fingerprint] {
				result.Duplicates++
				continue
			}
			ciphertext, err := s.encryption.Encrypt(encoded)
			if err != nil {
				return app_errors.ErrInternalServer
			}
			endpoint, err := url.Parse(config.URL)
			if err != nil {
				return app_errors.ErrValidation
			}
			row := models.Proxy{Name: catalog.SuggestedName(endpoint.Host), Config: ciphertext, Fingerprint: fingerprint, Enabled: true}
			if err := tx.Create(&row).Error; err != nil {
				return proxyDatabaseError(err)
			}
			catalog[row.ID] = stateloader.ManagedProxy{Row: row}
			fingerprints[fingerprint] = true
			result.Imported++
		}
		return nil
	})
	return result, err
}

func validateProxyTestURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	endpoint, err := url.Parse(value)
	if err != nil || len(value) > 2048 || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", app_errors.ErrValidation
	}
	if _, err := http.NewRequest(http.MethodGet, value, nil); err != nil {
		return "", app_errors.ErrValidation
	}
	return value, nil
}

func loadProxyTestURL(ctx context.Context, db *gorm.DB) (string, error) {
	var row models.SystemSetting
	err := db.WithContext(ctx).Where(&models.SystemSetting{Key: proxyTestURLSetting}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultProxyTestURL, nil
	}
	if err != nil {
		return "", proxyDatabaseError(err)
	}
	return row.Value, nil
}

func (s *Service) SaveProxyTestURL(ctx context.Context, value string) error {
	value, err := validateProxyTestURL(value)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		return proxyDatabaseError(tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at_ms"})}).Create(&models.SystemSetting{Key: proxyTestURLSetting, Value: value}).Error)
	})
}

var proxyTestSlots = make(chan struct{}, 8)

func (s *Service) TestProxy(ctx context.Context, id uint, target string) (ProxyItem, error) {
	target, err := validateProxyTestURL(target)
	if err != nil {
		return ProxyItem{}, err
	}
	select {
	case proxyTestSlots <- struct{}{}:
		defer func() { <-proxyTestSlots }()
	case <-ctx.Done():
		return ProxyItem{}, ctx.Err()
	}
	var row models.Proxy
	if err := s.db.WithContext(ctx).Take(&row, id).Error; err != nil {
		return ProxyItem{}, proxyDatabaseError(err)
	}
	config, err := decryptProxyOverride(s.encryption, &row.Config)
	if err != nil || config == nil || config.Mode != outboundproxy.ModeCustom || config.ProxyID != 0 {
		return ProxyItem{}, app_errors.ErrInternalServer
	}
	// 手动测试刻意绕过启用状态与继承，只访问被指定的代理。
	network, err := s.proxyNetworkContext(outboundproxy.Effective{Config: *config, Source: outboundproxy.SourceGlobal})
	if err != nil {
		return ProxyItem{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := subscriptionruntime.HTTPClient(subscriptionruntime.WithNetworkContext(probeCtx, network))
	if err != nil {
		return ProxyItem{}, app_errors.ErrInternalServer
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return ProxyItem{}, app_errors.ErrValidation
	}
	started := time.Now()
	response, requestErr := client.Do(request)
	duration := time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return ProxyItem{}, ctx.Err()
	}
	status := 0
	errorCode := ""
	if requestErr != nil {
		errorCode = "connection_failed"
		var netErr net.Error
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) || errors.As(requestErr, &netErr) && netErr.Timeout() {
			errorCode = "timeout"
		}
	} else {
		status = response.StatusCode
		_ = response.Body.Close()
	}
	at := s.now().UnixMilli()
	updates := map[string]any{"last_test_url": target, "last_test_at_ms": at, "last_test_duration_ms": duration, "last_test_status_code": status, "last_test_error": errorCode}
	if err := s.db.WithContext(ctx).Model(&models.Proxy{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return ProxyItem{}, proxyDatabaseError(err)
	}
	row.LastTestURL, row.LastTestAtMS, row.LastTestDurationMS, row.LastTestStatusCode, row.LastTestError = target, &at, &duration, &status, errorCode
	return proxyItem(stateloader.ManagedProxy{Row: row, Config: *config}, ProxyReferences{})
}

func proxyDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	return app_errors.ParseDBError(err)
}
