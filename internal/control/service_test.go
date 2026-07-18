package control

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestWriteConfigRollsBackWhenCompileRejectsCandidate(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision

	_, err := fixture.service.writeConfig(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&models.Group{
			Name: "invalid", UpstreamURL: "https://invalid.example",
			Signature: "invalid", Protocols: models.JSON(`[]`),
			Models: models.JSON(`[]`), Config: models.JSON(`{}`), Enabled: true,
		}).Error
	}, nil)
	if err == nil {
		t.Fatal("writeConfig() error = nil, want Compile rejection")
	}
	assertGroupCount(t, fixture.db, 0)
	if got := fixture.manager.Current().Revision; got != before {
		t.Fatalf("revision = %d, want %d", got, before)
	}
}

func TestWriteConfigPublishesOnceBeforeCallbackWhileHoldingLock(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	callbackRevision := make(chan uint64, 1)

	snapshot, err := fixture.service.writeConfig(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(validControlGroup("published")).Error
	}, func() error {
		if fixture.service.writeMu.TryLock() {
			fixture.service.writeMu.Unlock()
			return fmt.Errorf("writeMu was not held during afterPublish")
		}
		callbackRevision <- fixture.manager.Current().Revision
		return nil
	})
	if err != nil {
		t.Fatalf("writeConfig() error = %v", err)
	}
	if snapshot.Revision != before+1 {
		t.Fatalf("snapshot revision = %d, want %d", snapshot.Revision, before+1)
	}
	if got := <-callbackRevision; got != snapshot.Revision {
		t.Fatalf("callback revision = %d, want %d", got, snapshot.Revision)
	}
	assertGroupCount(t, fixture.db, 1)
}

func TestWriteConfigSerializesConcurrentDatabaseAndSnapshotPublication(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	start := make(chan struct{})
	errors := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)

	for _, name := range []string{"first", "second"} {
		name := name
		go func() {
			ready.Done()
			<-start
			_, err := fixture.service.writeConfig(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(validControlGroup(name)).Error
			}, nil)
			errors <- err
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent writeConfig() error = %v", err)
		}
	}

	assertGroupCount(t, fixture.db, 2)
	snapshot := fixture.manager.Current()
	if snapshot.Revision != before+2 {
		t.Fatalf("revision = %d, want %d", snapshot.Revision, before+2)
	}
	if len(snapshot.Groups) != 2 {
		t.Fatalf("snapshot groups = %#v, want two", snapshot.Groups)
	}
}

func TestConcurrentImportsPublishDatabaseTruth(t *testing.T) {
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	requests := []ImportRequest{
		{UpstreamURL: "https://shared.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-shared-a"},
		{UpstreamURL: "https://shared.example.com/v1", Protocols: []protocol.Protocol{protocol.Anthropic}, Keys: "sk-shared-b"},
		{UpstreamURL: "https://one.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-one"},
		{UpstreamURL: "https://two.example.com/v1", Protocols: []protocol.Protocol{protocol.Gemini}, Keys: "sk-two"},
		{UpstreamURL: "https://three.example.com/v1", Protocols: []protocol.Protocol{protocol.OpenAI}, Keys: "sk-three"},
		{UpstreamURL: "https://four.example.com/v1", Protocols: []protocol.Protocol{protocol.Anthropic}, Keys: "sk-four"},
	}

	start := make(chan struct{})
	errors := make(chan error, len(requests))
	var ready sync.WaitGroup
	ready.Add(len(requests))
	for _, request := range requests {
		request := request
		go func() {
			ready.Done()
			<-start
			_, err := fixture.service.Import(context.Background(), request)
			errors <- err
		}()
	}
	ready.Wait()
	close(start)
	for range requests {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent Import() error = %v", err)
		}
	}

	input, err := stateloader.BuildCompileInput(context.Background(), fixture.db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	want, err := state.Compile(input)
	if err != nil {
		t.Fatalf("Compile(DB input) error = %v", err)
	}
	got := fixture.manager.Current()
	want.Revision = got.Revision
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published snapshot differs from DB compile\ngot=%#v\nwant=%#v", got, want)
	}
	if got.Revision != before+uint64(len(requests)) {
		t.Fatalf("revision = %d, want %d", got.Revision, before+uint64(len(requests)))
	}
}

func validControlGroup(name string) *models.Group {
	return &models.Group{
		Name: name, UpstreamURL: "https://" + name + ".example/v1",
		Signature: name + "-signature", Protocols: models.JSON(`["openai"]`),
		Models: models.JSON(`[{"id":"gpt-4o"}]`), Config: models.JSON(`{}`), Enabled: true,
	}
}
