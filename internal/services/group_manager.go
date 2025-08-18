package services

import (
	"context"
	"encoding/json"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"gpt-load/internal/syncer"
	"gpt-load/internal/utils"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const GroupUpdateChannel = "groups:updated"

// GroupManager manages the caching of group data.
type GroupManager struct {
	syncer          *syncer.CacheSyncer[map[string]*models.Group]
	db              *gorm.DB
	store           store.Store
	settingsManager *config.SystemSettingsManager
}

// NewGroupManager creates a new, uninitialized GroupManager.
func NewGroupManager(
	db *gorm.DB,
	store store.Store,
	settingsManager *config.SystemSettingsManager,
) *GroupManager {
	return &GroupManager{
		db:              db,
		store:           store,
		settingsManager: settingsManager,
	}
}

// Initialize sets up the CacheSyncer. This is called separately to handle potential
func (gm *GroupManager) Initialize() error {
	loader := func() (map[string]*models.Group, error) {
		var groups []*models.Group
		if err := gm.db.Find(&groups).Error; err != nil {
			return nil, fmt.Errorf("failed to load groups from db: %w", err)
		}

		groupMap := make(map[string]*models.Group, len(groups))
		for _, group := range groups {
			g := *group
			g.EffectiveConfig = gm.settingsManager.GetEffectiveConfig(g.Config)
			g.ProxyKeysMap = utils.StringToSet(g.ProxyKeys, ",")

			// Parse header rules with error handling
			if len(group.HeaderRules) > 0 {
				if err := json.Unmarshal(group.HeaderRules, &g.HeaderRuleList); err != nil {
					logrus.WithError(err).WithField("group_name", g.Name).Warn("Failed to parse header rules for group")
					g.HeaderRuleList = []models.HeaderRule{}
				}
			} else {
				g.HeaderRuleList = []models.HeaderRule{}
			}

			groupMap[g.Name] = &g
			logrus.WithFields(logrus.Fields{
				"group_name":         g.Name,
				"effective_config":   g.EffectiveConfig,
				"header_rules_count": len(g.HeaderRuleList),
			}).Debug("Loaded group with effective config")
		}

		return groupMap, nil
	}

	syncer, err := syncer.NewCacheSyncer(
		loader,
		gm.store,
		GroupUpdateChannel,
		logrus.WithField("syncer", "groups"),
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create group syncer: %w", err)
	}
	gm.syncer = syncer
	return nil
}

// GetGroupByName retrieves a single group by its name from the cache.
func (gm *GroupManager) GetGroupByName(name string) (*models.Group, error) {
	if gm.syncer == nil {
		return nil, fmt.Errorf("GroupManager is not initialized")
	}

	groups := gm.syncer.Get()
	group, ok := groups[name]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return group, nil
}

// Invalidate triggers a cache reload across all instances.
func (gm *GroupManager) Invalidate() error {
	if gm.syncer == nil {
		return fmt.Errorf("GroupManager is not initialized")
	}
	return gm.syncer.Invalidate()
}

// Stop gracefully stops the GroupManager's background syncer.
func (gm *GroupManager) Stop(ctx context.Context) {
	if gm.syncer != nil {
		gm.syncer.Stop()
	}
}
