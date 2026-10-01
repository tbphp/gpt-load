package control

import (
	"context"
	"errors"
	"net/url"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func normalizeProxyOverride(
	field optionalField[outboundproxy.Config],
	encryptionService encryption.Service,
) (*string, bool, error) {
	if !field.Set {
		return nil, false, nil
	}
	if field.Null {
		return nil, true, nil
	}
	config, err := outboundproxy.Normalize(field.Value)
	if err != nil || config.Mode == outboundproxy.ModeInherit {
		return nil, false, app_errors.ErrValidation
	}
	if encryptionService == nil {
		return nil, false, app_errors.ErrInternalServer
	}
	encoded, err := outboundproxy.Encode(config)
	if err != nil {
		return nil, false, app_errors.ErrValidation
	}
	ciphertext, err := encryptionService.Encrypt(encoded)
	if err != nil {
		return nil, false, app_errors.ErrInternalServer
	}
	return &ciphertext, true, nil
}

// 新选择必须指向已启用代理；既有失效引用只有显式改选时才校验。
func validateProxySelection(db *gorm.DB, config outboundproxy.Config) error {
	if config.ProxyID == 0 {
		return nil
	}
	var row models.Proxy
	if err := db.Select("id", "enabled").Take(&row, config.ProxyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return app_errors.ErrValidation
		}
		return app_errors.ParseDBError(err)
	}
	if !row.Enabled {
		return app_errors.ErrValidation
	}
	return nil
}

// managedProxyOverride 兼容旧 URL 写入，并在同一配置事务内去重为集中引用。
func (s *Service) managedProxyOverride(ctx context.Context, db *gorm.DB, stored *string) (*string, error) {
	config, err := decryptProxyOverride(s.encryption, stored)
	if err != nil {
		return nil, err
	}
	if config == nil || config.Mode != outboundproxy.ModeCustom {
		return stored, nil
	}
	if config.ProxyID != 0 {
		return stored, validateProxySelection(db, *config)
	}
	identity, err := outboundproxy.CanonicalConnection(*config)
	if err != nil {
		return nil, app_errors.ErrValidation
	}
	encoded, err := outboundproxy.Encode(identity)
	if err != nil {
		return nil, app_errors.ErrValidation
	}
	catalog, err := stateloader.LoadProxyCatalog(ctx, db, s.encryption)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	fingerprint := s.encryption.Hash(encoded)
	var row models.Proxy
	for _, entry := range catalog {
		if entry.Row.Fingerprint == fingerprint {
			row = entry.Row
			break
		}
	}
	if row.ID == 0 {
		endpoint, err := url.Parse(config.URL)
		if err != nil {
			return nil, app_errors.ErrValidation
		}
		row = models.Proxy{Name: catalog.SuggestedName(endpoint.Host), Config: *stored, Fingerprint: fingerprint, Enabled: true}
		if err := db.Create(&row).Error; err != nil {
			return nil, app_errors.ParseDBError(err)
		}
	}
	if !row.Enabled {
		return nil, app_errors.ErrValidation
	}
	ciphertext, _, err := normalizeProxyOverride(optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: row.ID}}, s.encryption)
	return ciphertext, err
}

func (s *Service) globalNetworkContext(
	ctx context.Context,
	db *gorm.DB,
) (subscriptionruntime.NetworkContext, error) {
	global, err := s.loadGlobalProxyConfig(ctx, db)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, err
	}
	effective, err := outboundproxy.Resolve(nil, nil, global, s.environmentProxy)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	return s.proxyNetworkContext(effective)
}

func (s *Service) draftNetworkContext(
	ctx context.Context,
	config *outboundproxy.Config,
) (subscriptionruntime.NetworkContext, error) {
	if config == nil {
		return s.globalNetworkContext(ctx, s.db)
	}
	normalized, err := outboundproxy.Normalize(*config)
	if err != nil || normalized.Mode == outboundproxy.ModeInherit {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrValidation
	}
	resolved, err := s.resolveManagedProxy(ctx, s.db, &normalized)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, err
	}
	if resolved == nil {
		return s.globalNetworkContext(ctx, s.db)
	}
	effective, err := outboundproxy.Resolve(nil, resolved, nil, nil)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrValidation
	}
	return s.proxyNetworkContext(effective)
}

func (s *Service) groupNetworkContext(
	ctx context.Context,
	db *gorm.DB,
	group models.Group,
) (subscriptionruntime.NetworkContext, error) {
	_, effective, err := s.resolveGroupProxy(ctx, db, group)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, err
	}
	return s.proxyNetworkContext(effective)
}

func (s *Service) groupCredentialStageNetworkContext(
	ctx context.Context,
	groupID uint,
	channelID channel.ID,
) (subscriptionruntime.NetworkContext, error) {
	if s == nil || s.db == nil || groupID == 0 || channelID == "" {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrValidation
	}
	db := s.db.WithContext(ctx)
	group, err := loadGroupRow(db, groupID)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, err
	}
	if group.ChannelID != string(channelID) || group.ConnectionType != models.ConnectionTypeSubscription {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrValidation
	}
	return s.groupNetworkContext(ctx, db, group)
}

func (s *Service) credentialNetworkContext(
	ctx context.Context,
	db *gorm.DB,
	group models.Group,
	credential models.Credential,
) (subscriptionruntime.NetworkContext, error) {
	configured, err := s.resolvedProxyOverride(ctx, db, credential.ProxyConfig)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, err
	}
	if configured == nil {
		return s.groupNetworkContext(ctx, db, group)
	}
	effective, err := outboundproxy.Resolve(configured, nil, nil, nil)
	if err != nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	return s.proxyNetworkContext(effective)
}

func (s *Service) proxyNetworkContext(
	effective outboundproxy.Effective,
) (subscriptionruntime.NetworkContext, error) {
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil || s.encryption == nil {
		return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
	}
	identity := ""
	if effective.Config.Mode == outboundproxy.ModeEnvironment {
		identity = `{"mode":"environment"}`
	} else {
		identity, err = outboundproxy.Encode(effective.Config)
		if err != nil {
			return subscriptionruntime.NetworkContext{}, app_errors.ErrInternalServer
		}
	}
	return subscriptionruntime.NetworkContext{
		Proxy: effective, Fingerprint: s.encryption.Hash(identity),
	}, nil
}

func decryptProxyOverride(
	encryptionService encryption.Service,
	ciphertext *string,
) (*outboundproxy.Config, error) {
	if ciphertext == nil {
		return nil, nil
	}
	if encryptionService == nil || *ciphertext == "" {
		return nil, app_errors.ErrInternalServer
	}
	plaintext, err := encryptionService.Decrypt(*ciphertext)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	config, err := outboundproxy.Decode(plaintext)
	plaintext = ""
	if err != nil || config.Mode == outboundproxy.ModeInherit {
		return nil, app_errors.ErrInternalServer
	}
	return &config, nil
}

func storedProxyIdentity(
	encryptionService encryption.Service,
	ciphertext *string,
) (string, string, error) {
	if ciphertext == nil {
		return "", "", nil
	}
	if encryptionService == nil || *ciphertext == "" {
		return "", "", app_errors.ErrInternalServer
	}
	plaintext, err := encryptionService.Decrypt(*ciphertext)
	if err != nil {
		return "", "", app_errors.ErrInternalServer
	}
	config, err := outboundproxy.Decode(plaintext)
	if err != nil || config.Mode == outboundproxy.ModeInherit {
		plaintext = ""
		return "", "", app_errors.ErrInternalServer
	}
	fingerprint := encryptionService.Hash(plaintext)
	plaintext = ""
	return *ciphertext, fingerprint, nil
}

func (s *Service) loadGlobalProxyConfig(
	ctx context.Context,
	db *gorm.DB,
) (*outboundproxy.Config, error) {
	var row models.SystemSetting
	err := globalProxyConfigScope(db.WithContext(ctx)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return s.resolvedProxyOverride(ctx, db, &row.Value)
}

func globalProxyConfigScope(db *gorm.DB) *gorm.DB {
	return db.Model(&models.SystemSetting{}).
		Select("key", "value").
		Where(&models.SystemSetting{Key: outboundproxy.SystemSettingKey})
}

func (s *Service) resolveGroupProxy(
	ctx context.Context,
	db *gorm.DB,
	group models.Group,
) (*outboundproxy.Config, outboundproxy.Effective, error) {
	configured, err := decryptProxyOverride(s.encryption, group.ProxyConfig)
	if err != nil {
		return nil, outboundproxy.Effective{}, err
	}
	global, err := s.loadGlobalProxyConfig(ctx, db)
	if err != nil {
		return nil, outboundproxy.Effective{}, err
	}
	resolved, err := s.resolveManagedProxy(ctx, db, configured)
	if err != nil {
		return nil, outboundproxy.Effective{}, err
	}
	effective, err := outboundproxy.Resolve(nil, resolved, global, s.environmentProxy)
	if err != nil {
		return nil, outboundproxy.Effective{}, app_errors.ErrInternalServer
	}
	return configured, effective, nil
}

func (s *Service) groupProxyView(
	ctx context.Context,
	db *gorm.DB,
	group models.Group,
) (outboundproxy.View, error) {
	configured, effective, err := s.resolveGroupProxy(ctx, db, group)
	if err != nil {
		return outboundproxy.View{}, err
	}
	view, err := outboundproxy.NewView(configured, effective)
	if err != nil {
		return outboundproxy.View{}, app_errors.ErrInternalServer
	}
	catalog, err := stateloader.LoadProxyCatalog(ctx, db, s.encryption)
	if err != nil {
		return outboundproxy.View{}, app_errors.ErrInternalServer
	}
	return catalog.Annotate(view, configured), nil
}

func (s *Service) credentialProxyViews(
	ctx context.Context,
	db *gorm.DB,
	group models.Group,
	rows []models.Credential,
) (map[uint]outboundproxy.View, error) {
	_, parent, err := s.resolveGroupProxy(ctx, db, group)
	if err != nil {
		return nil, err
	}
	catalog, err := stateloader.LoadProxyCatalog(ctx, db, s.encryption)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	views := make(map[uint]outboundproxy.View, len(rows))
	for _, row := range rows {
		configured, err := decryptProxyOverride(s.encryption, row.ProxyConfig)
		if err != nil {
			return nil, err
		}
		effective := parent
		if resolved := catalog.Resolve(configured); resolved != nil {
			effective, err = outboundproxy.Resolve(resolved, nil, nil, nil)
			if err != nil {
				return nil, app_errors.ErrInternalServer
			}
		}
		view, err := outboundproxy.NewView(configured, effective)
		if err != nil {
			return nil, app_errors.ErrInternalServer
		}
		views[row.ID] = catalog.Annotate(view, configured)
	}
	return views, nil
}

func (s *Service) resolveManagedProxy(ctx context.Context, db *gorm.DB, config *outboundproxy.Config) (*outboundproxy.Config, error) {
	if config == nil || config.ProxyID == 0 {
		return config, nil
	}
	catalog, err := stateloader.LoadProxyCatalog(ctx, db, s.encryption)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	return catalog.Resolve(config), nil
}

func (s *Service) resolvedProxyOverride(ctx context.Context, db *gorm.DB, ciphertext *string) (*outboundproxy.Config, error) {
	config, err := decryptProxyOverride(s.encryption, ciphertext)
	if err != nil {
		return nil, err
	}
	return s.resolveManagedProxy(ctx, db, config)
}

func (s *Service) storedProxyIdentity(ctx context.Context, db *gorm.DB, ciphertext *string) (string, string, error) {
	configured, err := decryptProxyOverride(s.encryption, ciphertext)
	if err != nil {
		return "", "", err
	}
	if configured == nil || configured.ProxyID == 0 {
		return storedProxyIdentity(s.encryption, ciphertext)
	}
	catalog, err := stateloader.LoadProxyCatalog(ctx, db, s.encryption)
	if err != nil {
		return "", "", app_errors.ErrInternalServer
	}
	proxy, exists := catalog[configured.ProxyID]
	if !exists || !proxy.Row.Enabled {
		return "", "", nil
	}
	return proxy.Row.Config, proxy.RuntimeFingerprint, nil
}
