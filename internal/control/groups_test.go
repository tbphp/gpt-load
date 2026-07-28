package control

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestListGroupsUsesSingleReadSnapshot(t *testing.T) {
	fixture, dsn := newFileServiceFixture(t)
	group := validControlGroup("list-snapshot-old")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.UpstreamKey{
		GroupID:  group.ID,
		KeyValue: "cipher-old",
		KeyHash:  "hash-old",
		Status:   models.UpstreamKeyStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	writer, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open(writer) error = %v", err)
	}
	writerSQL, err := writer.DB()
	if err != nil {
		t.Fatalf("writer.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := writerSQL.Close(); err != nil {
			t.Errorf("close writer database: %v", err)
		}
	})

	firstSelect := make(chan struct{})
	releaseReader := make(chan struct{})
	var barrierOnce sync.Once
	const callbackName = "test:list_groups_single_read_snapshot"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Table != "groups" {
				return
			}
			barrierOnce.Do(func() {
				close(firstSelect)
				<-releaseReader
			})
		},
	); err != nil {
		t.Fatalf("register ListGroups query barrier: %v", err)
	}

	listDone := make(chan struct {
		groups []GroupResponse
		err    error
	}, 1)
	go func() {
		groups, err := fixture.service.ListGroups(t.Context())
		listDone <- struct {
			groups []GroupResponse
			err    error
		}{groups: groups, err: err}
	}()
	select {
	case <-firstSelect:
	case <-time.After(time.Second):
		t.Fatal("ListGroups did not pause after its first SELECT")
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).
				Update("name", "list-snapshot-new").Error; err != nil {
				return err
			}
			return tx.Create(&models.UpstreamKey{
				GroupID:  group.ID,
				KeyValue: "cipher-new-disabled",
				KeyHash:  "hash-new-disabled",
				Status:   models.UpstreamKeyStatusDisabled,
			}).Error
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("concurrent ListGroups version update error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListGroups read blocked WAL writer")
	}
	close(releaseReader)

	var result struct {
		groups []GroupResponse
		err    error
	}
	select {
	case result = <-listDone:
	case <-time.After(time.Second):
		t.Fatal("ListGroups did not finish after query barrier release")
	}
	if result.err != nil {
		t.Fatalf("ListGroups() error = %v", result.err)
	}
	if len(result.groups) != 1 {
		t.Fatalf("ListGroups() = %#v, want one Group", result.groups)
	}
	got := result.groups[0]
	oldVersion := got.Name == "list-snapshot-old" && got.KeyCount == 1
	newVersion := got.Name == "list-snapshot-new" && got.KeyCount == 2
	if !oldVersion && !newVersion {
		t.Fatalf("ListGroups observed mixed versions: %#v", got)
	}
}

func TestListGroupsMapsAfterReadSnapshot(t *testing.T) {
	fixture, _ := newFileServiceFixture(t)
	group := validControlGroup("list-map-after-snapshot")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}

	rows, err := fixture.service.readListGroupsSnapshot(t.Context())
	if err != nil {
		t.Fatalf("readListGroupsSnapshot() error = %v", err)
	}
	if err := fixture.db.Model(&models.Group{}).Where("id = ?", group.ID).
		Update("name", "write-after-list-read").Error; err != nil {
		t.Fatalf("single-pool write after read snapshot error = %v", err)
	}
	got, err := mapListGroupsSnapshot(rows)
	if err != nil {
		t.Fatalf("mapListGroupsSnapshot() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "list-map-after-snapshot" {
		t.Fatalf("mapped rows = %#v, want owned pre-write Group copy", got)
	}
}

func TestListGroupsReturnsAllGroupsAndTotalKeyCountsInTwoQueries(t *testing.T) {
	fixture := newServiceFixture(t)
	enabled := validControlGroup("enabled")
	enabled.Models = models.JSON(`[{"id":"gpt-4o","alias":"Primary"}]`)
	if err := fixture.db.Create(enabled).Error; err != nil {
		t.Fatalf("create enabled group: %v", err)
	}
	disabled := validControlGroup("disabled")
	if err := fixture.db.Create(disabled).Error; err != nil {
		t.Fatalf("create disabled group: %v", err)
	}
	if err := fixture.db.Model(disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}
	for _, key := range []models.UpstreamKey{
		{GroupID: enabled.ID, KeyValue: "cipher-active", KeyHash: "hash-active", Status: models.UpstreamKeyStatusActive},
		{GroupID: enabled.ID, KeyValue: "cipher-disabled", KeyHash: "hash-disabled", Status: models.UpstreamKeyStatusDisabled},
	} {
		key := key
		if err := fixture.db.Create(&key).Error; err != nil {
			t.Fatalf("create upstream key: %v", err)
		}
	}

	queryCount := 0
	const callbackName = "test:list_groups_query_count"
	if err := fixture.db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	got, err := fixture.service.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}
	if queryCount != 2 {
		t.Fatalf("ListGroups() query count = %d, want 2", queryCount)
	}
	if len(got) != 2 {
		t.Fatalf("ListGroups() = %#v, want two groups", got)
	}
	if got[0].ID != enabled.ID || !got[0].Enabled || got[0].KeyCount != 2 {
		t.Fatalf("enabled response = %#v, want enabled with two total keys", got[0])
	}
	if len(got[0].Protocols) != 1 || len(got[0].Models) != 1 || got[0].Models[0].Alias != "Primary" {
		t.Fatalf("enabled protocols/models = %#v/%#v", got[0].Protocols, got[0].Models)
	}
	if got[1].ID != disabled.ID || got[1].Enabled || got[1].KeyCount != 0 {
		t.Fatalf("disabled response = %#v, want disabled with zero keys", got[1])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(groups) error = %v", err)
	}
	for _, forbidden := range []string{
		"key_value", "key_hash", "cipher-active", "cipher-disabled", "hash-active", "hash-disabled",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("groups response exposes %q: %s", forbidden, encoded)
		}
	}
}
