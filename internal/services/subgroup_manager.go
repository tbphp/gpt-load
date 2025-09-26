package services

import (
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/store"
	"sync"

	"github.com/sirupsen/logrus"
)

// SubGroupManager manages weighted round-robin selection for all aggregate groups
type SubGroupManager struct {
	store     store.Store
	selectors map[uint]*selector
	mu        sync.RWMutex
}

// subGroupItem represents a sub-group with its weight and current weight for round-robin
type subGroupItem struct {
	group         *models.Group
	weight        int
	currentWeight int
}

// NewSubGroupManager creates a new sub-group manager service
func NewSubGroupManager(store store.Store) *SubGroupManager {
	return &SubGroupManager{
		store:     store,
		selectors: make(map[uint]*selector),
	}
}

// SelectSubGroup selects an appropriate sub-group for the given aggregate group
func (m *SubGroupManager) SelectSubGroup(group *models.Group) (*models.Group, error) {
	if group.GroupType != "aggregate" {
		return group, nil
	}

	selector := m.getSelector(group)
	if selector == nil {
		return nil, fmt.Errorf("no valid sub-groups available for aggregate group '%s'", group.Name)
	}

	selected := selector.selectNext()
	if selected == nil {
		return nil, fmt.Errorf("no sub-groups with active keys for aggregate group '%s'", group.Name)
	}

	logrus.WithFields(logrus.Fields{
		"aggregate_group": group.Name,
		"selected_group":  selected.Name,
	}).Debug("Selected sub-group from aggregate")

	return selected, nil
}

// RebuildSelectors rebuild all selectors based on the incoming group
func (m *SubGroupManager) RebuildSelectors(groups map[string]*models.Group) {
	m.mu.Lock()
	m.selectors = make(map[uint]*selector)
	m.mu.Unlock()

	rebuildCount := 0
	for _, group := range groups {
		if group.GroupType == "aggregate" && len(group.SubGroups) > 0 {
			if sel := m.getSelector(group); sel != nil {
				rebuildCount++
			}
		}
	}

	logrus.WithField("new_count", rebuildCount).Debug("Rebuilt selectors for aggregate groups")
}

// getSelector retrieves or creates a selector for the aggregate group
func (m *SubGroupManager) getSelector(group *models.Group) *selector {
	m.mu.RLock()
	if sel, exists := m.selectors[group.ID]; exists {
		m.mu.RUnlock()
		return sel
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if sel, exists := m.selectors[group.ID]; exists {
		return sel
	}

	sel := m.createSelector(group)
	if sel != nil {
		m.selectors[group.ID] = sel
		logrus.WithFields(logrus.Fields{
			"group_id":        group.ID,
			"group_name":      group.Name,
			"sub_group_count": len(sel.subGroups),
		}).Debug("Created sub-group selector")
	}

	return sel
}

// createSelector creates a new selector for an aggregate group
func (m *SubGroupManager) createSelector(group *models.Group) *selector {
	if group.GroupType != "aggregate" || len(group.SubGroups) == 0 {
		return nil
	}

	var items []subGroupItem
	for _, sg := range group.SubGroups {
		items = append(items, subGroupItem{
			group:         &sg.SubGroup,
			weight:        sg.Weight,
			currentWeight: 0,
		})
	}

	if len(items) == 0 {
		return nil
	}

	return &selector{
		groupID:   group.ID,
		groupName: group.Name,
		subGroups: items,
		store:     m.store,
	}
}

// selector encapsulates the weighted round-robin algorithm for a single aggregate group
type selector struct {
	groupID   uint
	groupName string
	subGroups []subGroupItem
	store     store.Store
	mu        sync.Mutex
}

// selectNext uses weighted round-robin algorithm to select a sub-group with active keys
func (s *selector) selectNext() *models.Group {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.subGroups) == 0 {
		return nil
	}

	if len(s.subGroups) == 1 {
		if s.hasActiveKeys(s.subGroups[0].group.ID) {
			return s.subGroups[0].group
		}
		logrus.WithFields(logrus.Fields{
			"group_id":   s.subGroups[0].group.ID,
			"group_name": s.subGroups[0].group.Name,
		}).Debug("Single sub-group has no active keys")
		return nil
	}

	attempted := make(map[uint]bool)
	for len(attempted) < len(s.subGroups) {
		item := s.selectByWeight()
		if item == nil {
			break
		}

		if attempted[item.group.ID] {
			continue
		}
		attempted[item.group.ID] = true

		if s.hasActiveKeys(item.group.ID) {
			logrus.WithFields(logrus.Fields{
				"aggregate_group": s.groupName,
				"selected_group":  item.group.Name,
				"attempts":        len(attempted),
			}).Debug("Selected sub-group with active keys")
			return item.group
		}

		logrus.WithFields(logrus.Fields{
			"group_id":   item.group.ID,
			"group_name": item.group.Name,
			"attempts":   len(attempted),
		}).Debug("Sub-group has no active keys, trying next")
	}

	logrus.WithFields(logrus.Fields{
		"aggregate_group":  s.groupName,
		"total_sub_groups": len(s.subGroups),
	}).Warn("No sub-groups with active keys available")

	return nil
}

// selectByWeight implements smooth weighted round-robin algorithm
func (s *selector) selectByWeight() *subGroupItem {
	totalWeight := 0
	var best *subGroupItem

	for i := range s.subGroups {
		item := &s.subGroups[i]
		totalWeight += item.weight
		item.currentWeight += item.weight

		if best == nil || item.currentWeight > best.currentWeight {
			best = item
		}
	}

	if best == nil {
		return &s.subGroups[0]
	}

	best.currentWeight -= totalWeight
	return best
}

// hasActiveKeys checks if a sub-group has available API keys
func (s *selector) hasActiveKeys(groupID uint) bool {
	key := fmt.Sprintf("group:%d:active_keys", groupID)
	length, err := s.store.LLen(key)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"group_id": groupID,
			"error":    err,
		}).Debug("Error checking active keys, assuming available")
		return true
	}
	return length > 0
}
