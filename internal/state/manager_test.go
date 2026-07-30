package state

import (
	"sync"
	"testing"
	"time"

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

func TestManagerMatchesCompiledInputWithoutChangingRevision(t *testing.T) {
	manager := NewManager()
	input := managerCompileInput(1)
	published, err := manager.Publish(input)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	matches, err := manager.Matches(input)
	if err != nil {
		t.Fatalf("Matches(same) error = %v", err)
	}
	if !matches {
		t.Fatal("Matches(same) = false, want true")
	}
	if current := manager.Current(); current != published || current.Revision != 1 {
		t.Fatalf("Matches() changed current snapshot: %#v", current)
	}

	changed := managerCompileInput(2)
	matches, err = manager.Matches(changed)
	if err != nil {
		t.Fatalf("Matches(changed) error = %v", err)
	}
	if matches {
		t.Fatal("Matches(changed) = true, want false")
	}

	invalid := managerCompileInput(1)
	invalid.Groups[0].Protocols = []protocol.Protocol{"reserved"}
	if _, err := manager.Matches(invalid); err == nil {
		t.Fatal("Matches(invalid) error = nil, want compile rejection")
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

func TestManagerWithCurrentSnapshotHandlesNilAndReturnsCallbackResult(t *testing.T) {
	var nilManager *Manager
	if nilManager.WithCurrentSnapshot(func(*ConfigSnapshot) bool { return true }) {
		t.Fatal("nil Manager.WithCurrentSnapshot() = true, want false")
	}

	manager := NewManager()
	if manager.WithCurrentSnapshot(nil) {
		t.Fatal("WithCurrentSnapshot(nil) = true, want false")
	}
	published, err := manager.Publish(managerCompileInput(1))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	var callbackSnapshot *ConfigSnapshot
	if got := manager.WithCurrentSnapshot(func(snapshot *ConfigSnapshot) bool {
		callbackSnapshot = snapshot
		return true
	}); !got {
		t.Fatal("WithCurrentSnapshot(true callback) = false, want true")
	}
	if callbackSnapshot != published {
		t.Fatalf("callback snapshot = %p, want current %p", callbackSnapshot, published)
	}
	if got := manager.WithCurrentSnapshot(func(*ConfigSnapshot) bool {
		return false
	}); got {
		t.Fatal("WithCurrentSnapshot(false callback) = true, want false")
	}
}

func TestManagerWithCurrentSnapshotBlocksConcurrentPublishUntilCallbackReturns(t *testing.T) {
	manager := NewManager()
	first, err := manager.Publish(managerCompileInput(1))
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}

	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	callbackReturned := make(chan struct{})
	withCurrentDone := make(chan bool, 1)
	go func() {
		withCurrentDone <- manager.WithCurrentSnapshot(func(snapshot *ConfigSnapshot) bool {
			if snapshot != first {
				t.Errorf("callback snapshot = %p, want first %p", snapshot, first)
			}
			close(callbackEntered)
			<-releaseCallback
			close(callbackReturned)
			return true
		})
	}()
	awaitManagerSignal(t, callbackEntered, "WithCurrentSnapshot callback entry")

	next, err := Compile(managerCompileInput(2))
	if err != nil {
		t.Fatalf("Compile(next snapshot) error = %v", err)
	}
	publishLockAttempted := make(chan struct{})
	publishReturned := make(chan *ConfigSnapshot, 1)
	go func() {
		publishReturned <- manager.publishCompiled(next, func() {
			close(publishLockAttempted)
		})
	}()
	awaitManagerSignal(t, publishLockAttempted, "concurrent publishMu lock attempt")

	if manager.publishMu.TryLock() {
		manager.publishMu.Unlock()
		t.Fatal("publishMu was unlocked while WithCurrentSnapshot callback was blocked")
	}
	select {
	case published := <-publishReturned:
		t.Fatalf("Publish() returned snapshot %p while callback was blocked", published)
	default:
	}

	close(releaseCallback)
	awaitManagerSignal(t, callbackReturned, "WithCurrentSnapshot callback return")
	select {
	case got := <-withCurrentDone:
		if !got {
			t.Fatal("WithCurrentSnapshot() = false, want callback result true")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WithCurrentSnapshot to return")
	}

	select {
	case published := <-publishReturned:
		if published.Revision != 2 {
			t.Fatalf("concurrent Publish().Revision = %d, want 2", published.Revision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for concurrent Publish to return")
	}
}

func awaitManagerSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func managerCompileInput(groupID uint) CompileInput {
	return CompileInput{Groups: []GroupConfig{{
		ID:          groupID,
		Name:        "group",
		UpstreamURL: "https://upstream.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAIChatCompletions},
		Models:      []ModelConfig{{ID: "model"}},
		Enabled:     true,
	}}}
}
