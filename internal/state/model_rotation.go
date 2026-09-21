package state

// GroupModelKey 将轮询进度限定在同组同名模型，组内凭据共享进度。
type GroupModelKey struct {
	GroupID       uint
	ExternalModel string
}

// SelectModel 接收按上游 ID 排序的可用模型；只消费模型轮次，不修改凭据份额。
func (s *SchedulingState) SelectModel(groupID uint, externalModel string, models []string) string {
	if len(models) == 0 {
		return ""
	}
	selected := models[0]
	s.WithLock(func(ledger *SchedulingLedger) {
		key := GroupModelKey{GroupID: groupID, ExternalModel: externalModel}
		last, tracked := ledger.ModelCursors[key]
		for _, model := range models {
			if model > last {
				selected = model
				break
			}
		}
		if tracked {
			ledger.ModelCursors[key] = selected
		}
	})
	return selected
}

func (s *SchedulingState) syncModelCursorsLocked(snapshot *ConfigSnapshot) {
	cursors := make(map[GroupModelKey]string)
	for key, last := range s.ledger.ModelCursors {
		if group, exists := snapshot.GroupCatalog[key.GroupID]; exists && !group.Enabled {
			cursors[key] = last
		}
	}
	for groupID, group := range snapshot.Groups {
		counts := make(map[string]int)
		for _, model := range group.Models {
			counts[externalModelName(model)]++
		}
		for name, count := range counts {
			if count > 1 {
				key := GroupModelKey{GroupID: groupID, ExternalModel: name}
				cursors[key] = s.ledger.ModelCursors[key]
			}
		}
	}
	s.ledger.ModelCursors = cursors
}
