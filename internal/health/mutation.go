package health

import "sync"

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
