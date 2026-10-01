package loader

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/storage/models"
)

// ManagedProxy 仅供配置加载与控制面解析；数据面仍接收已展开的旧契约。
type ManagedProxy struct {
	Row                models.Proxy
	Config             outboundproxy.Config
	RuntimeFingerprint string
}

type ProxyCatalog map[uint]ManagedProxy

// SuggestedName 让自动导入的同地址、不同认证代理可区分，不暴露用户名或密码。
func (catalog ProxyCatalog) SuggestedName(host string) string {
	base := strings.ToValidUTF8(host[:min(len(host), 240)], "")
	used := make(map[string]bool, len(catalog))
	for _, entry := range catalog {
		used[entry.Row.Name] = true
	}
	name := base
	for number := 2; used[name]; number++ {
		name = fmt.Sprintf("%s (%d)", base, number)
	}
	return name
}

func LoadProxyCatalog(ctx context.Context, db *gorm.DB, crypto encryption.Service) (ProxyCatalog, error) {
	var rows []models.Proxy
	if err := db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query proxy catalog: %w", err)
	}
	return buildProxyCatalog(rows, crypto)
}

func buildProxyCatalog(rows []models.Proxy, crypto encryption.Service) (ProxyCatalog, error) {
	result := make(ProxyCatalog, len(rows))
	for _, row := range rows {
		if crypto == nil {
			return nil, fmt.Errorf("proxy encryption is unavailable")
		}
		plaintext, err := crypto.Decrypt(row.Config)
		if err != nil {
			return nil, fmt.Errorf("decrypt managed proxy %d", row.ID)
		}
		config, err := outboundproxy.Decode(plaintext)
		if err != nil || config.Mode != outboundproxy.ModeCustom || config.ProxyID != 0 {
			return nil, fmt.Errorf("invalid managed proxy %d", row.ID)
		}
		identity, err := outboundproxy.CanonicalConnection(config)
		if err != nil {
			return nil, fmt.Errorf("invalid managed proxy identity")
		}
		canonical, err := outboundproxy.Encode(identity)
		if err != nil || crypto.Hash(canonical) != row.Fingerprint {
			return nil, fmt.Errorf("managed proxy %d identity mismatch", row.ID)
		}
		result[row.ID] = ManagedProxy{Row: row, Config: config, RuntimeFingerprint: crypto.Hash(plaintext)}
	}
	return result, nil
}

// Resolve 只对不存在或显式停用的引用回退；加载/解密错误由构造阶段返回。
func (catalog ProxyCatalog) Resolve(config *outboundproxy.Config) *outboundproxy.Config {
	if config == nil || config.ProxyID == 0 {
		return config
	}
	entry, exists := catalog[config.ProxyID]
	if !exists || !entry.Row.Enabled {
		return nil
	}
	value := entry.Config
	return &value
}

func (catalog ProxyCatalog) Annotate(view outboundproxy.View, configured *outboundproxy.Config) outboundproxy.View {
	if configured == nil || configured.ProxyID == 0 {
		return view
	}
	view.ProxyID = configured.ProxyID
	entry, exists := catalog[configured.ProxyID]
	if !exists {
		view.ReferenceState = "deleted"
		return view
	}
	view.ProxyName = entry.Row.Name
	view.ReferenceState = "enabled"
	if !entry.Row.Enabled {
		view.ReferenceState = "disabled"
	}
	return view
}

// MigrateLegacyProxyOverrides 在拥有原密钥后一次事务完成加密引用转换；重跑不改写引用。
func MigrateLegacyProxyOverrides(ctx context.Context, db *gorm.DB, crypto encryption.Service) error {
	if crypto == nil {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		catalog, err := LoadProxyCatalog(ctx, tx, crypto)
		if err != nil {
			return err
		}
		byFingerprint := make(map[string]uint, len(catalog))
		for id, value := range catalog {
			byFingerprint[value.Row.Fingerprint] = id
		}
		convert := func(ciphertext string) (string, bool, error) {
			plaintext, err := crypto.Decrypt(ciphertext)
			if err != nil {
				return "", false, fmt.Errorf("decrypt legacy proxy")
			}
			config, err := outboundproxy.Decode(plaintext)
			if err != nil {
				return "", false, fmt.Errorf("decode legacy proxy")
			}
			if config.Mode != outboundproxy.ModeCustom || config.ProxyID != 0 {
				return ciphertext, false, nil
			}
			identity, err := outboundproxy.CanonicalConnection(config)
			if err != nil {
				return "", false, err
			}
			canonical, err := outboundproxy.Encode(identity)
			if err != nil {
				return "", false, err
			}
			fingerprint := crypto.Hash(canonical)
			id := byFingerprint[fingerprint]
			if id == 0 {
				endpoint, err := url.Parse(config.URL)
				if err != nil {
					return "", false, fmt.Errorf("parse legacy proxy")
				}
				row := models.Proxy{Name: catalog.SuggestedName(endpoint.Host), Config: ciphertext, Fingerprint: fingerprint, Enabled: true}
				if err := tx.Create(&row).Error; err != nil {
					return "", false, err
				}
				id = row.ID
				byFingerprint[fingerprint] = id
				catalog[id] = ManagedProxy{Row: row}
			}
			reference, err := outboundproxy.Encode(outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: id})
			if err != nil {
				return "", false, err
			}
			encoded, err := crypto.Encrypt(reference)
			return encoded, true, err
		}
		var settings []models.SystemSetting
		if err := tx.Where(&models.SystemSetting{Key: outboundproxy.SystemSettingKey}).Find(&settings).Error; err != nil {
			return err
		}
		for _, row := range settings {
			value, changed, err := convert(row.Value)
			if err != nil {
				return err
			}
			if changed {
				if err := tx.Model(&models.SystemSetting{}).Where(&models.SystemSetting{Key: row.Key}).Update("value", value).Error; err != nil {
					return err
				}
			}
		}
		for _, table := range []string{"groups", "credentials"} {
			var rows []struct {
				ID          uint
				ProxyConfig string
			}
			if err := tx.Table(table).Select("id", "proxy_config").Where("proxy_config IS NOT NULL").Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				value, changed, err := convert(row.ProxyConfig)
				if err != nil {
					return err
				}
				if changed {
					if err := tx.Table(table).Where("id = ?", row.ID).Update("proxy_config", value).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
