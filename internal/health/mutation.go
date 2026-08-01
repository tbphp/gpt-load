package health

import (
	"sort"
	"sync"
)

const mutationStripeCount = 64

type MutationCoordinator struct {
	stripes [mutationStripeCount]sync.Mutex
}

func NewMutationCoordinator() *MutationCoordinator {
	return &MutationCoordinator{}
}

func (coordinator *MutationCoordinator) Do(keyID uint, fn func()) {
	if fn == nil {
		return
	}
	index := int(keyID % uint(len(coordinator.stripes)))
	coordinator.stripes[index].Lock()
	defer coordinator.stripes[index].Unlock()
	fn()
}

func (coordinator *MutationCoordinator) DoMany(keyIDs []uint, fn func()) {
	if fn == nil {
		return
	}
	selected := make(map[int]struct{}, len(keyIDs))
	indexes := make([]int, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		index := int(keyID % uint(len(coordinator.stripes)))
		if _, exists := selected[index]; exists {
			continue
		}
		selected[index] = struct{}{}
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		coordinator.stripes[index].Lock()
	}
	defer func() {
		for index := len(indexes) - 1; index >= 0; index-- {
			coordinator.stripes[indexes[index]].Unlock()
		}
	}()
	fn()
}
