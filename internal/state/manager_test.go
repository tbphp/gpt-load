package state

import (
	"sync"
	"testing"

	"gpt-load/internal/protocol"
)

func TestManagerPublishStartsAtRevisionOne(t *testing.T) {
	manager := NewManager()

	snapshot, err := manager.Publish(managerCompileInput(1))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if snapshot.Revision != 1 {
		t.Fatalf("Publish().Revision = %d, want 1", snapshot.Revision)
	}
	if current := manager.Current(); current != snapshot {
		t.Fatalf("Current() = %p, want published snapshot %p", current, snapshot)
	}
}

func TestManagerPublishPreservesOldSnapshotReference(t *testing.T) {
	manager := NewManager()
	first, err := manager.Publish(managerCompileInput(1))
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	second, err := manager.Publish(managerCompileInput(2))
	if err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}

	if second.Revision != 2 {
		t.Fatalf("second Publish().Revision = %d, want 2", second.Revision)
	}
	if first.Revision != 1 {
		t.Fatalf("first snapshot Revision = %d after second publish, want 1", first.Revision)
	}
	if _, ok := first.Groups[1]; !ok {
		t.Fatal("first snapshot reference changed after second publish")
	}
	if current := manager.Current(); current != second {
		t.Fatalf("Current() = %p, want second snapshot %p", current, second)
	}
}

func TestManagerPublishFailureKeepsCurrentSnapshot(t *testing.T) {
	manager := NewManager()
	first, err := manager.Publish(managerCompileInput(1))
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}

	invalid := managerCompileInput(2)
	invalid.Groups[0].Protocols = []protocol.Protocol{protocol.Protocol("invalid")}
	if _, err := manager.Publish(invalid); err == nil {
		t.Fatal("Publish() error = nil, want invalid protocol error")
	}
	if current := manager.Current(); current != first {
		t.Fatalf("Current() = %p after failed publish, want %p", current, first)
	}
}

func TestManagerConcurrentPublishAndCurrent(t *testing.T) {
	const goroutines = 32

	manager := NewManager()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		groupID := uint(i + 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := manager.Publish(managerCompileInput(groupID)); err != nil {
				t.Errorf("Publish(group %d) error = %v", groupID, err)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < goroutines; j++ {
				_ = manager.Current()
			}
		}()
	}
	close(start)
	wg.Wait()

	current := manager.Current()
	if current == nil {
		t.Fatal("Current() = nil after concurrent publishes")
	}
	if current.Revision != goroutines {
		t.Fatalf("Current().Revision = %d, want %d", current.Revision, goroutines)
	}
}

func managerCompileInput(groupID uint) CompileInput {
	return CompileInput{Groups: []GroupConfig{{
		ID:          groupID,
		Name:        "group",
		UpstreamURL: "https://upstream.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAI},
		Models:      []ModelConfig{{ID: "model"}},
		Enabled:     true,
	}}}
}
