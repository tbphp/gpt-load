package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func TestCreateGroupIdempotentReplaysOriginalCountsAndPreservesKeyMultiplicity(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x71}, 16))
	request := GroupCreateRequest{
		Name:        stringPointer("idempotent-group"),
		UpstreamURL: "https://idempotent.example.com/v1/",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{}},
		Keys:        " K \r\nK\n",
	}
	const key = "218f47a2-9c35-4d6e-8b1a-1234567890ab"

	first, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("first CreateGroupIdempotent() error = %v", err)
	}
	if first.KeysAdded != 1 || first.KeysDuplicated != 1 {
		t.Fatalf("first result = %#v", first)
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("replay CreateGroupIdempotent() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("replay = %#v, first = %#v", replayed, first)
	}
	var groupCount, keyCount int64
	if err := fixture.db.Model(&models.Group{}).Count(&groupCount).Error; err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if err := fixture.db.Model(&models.UpstreamKey{}).Count(&keyCount).Error; err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if groupCount != 1 || keyCount != 1 {
		t.Fatalf("resource counts = group:%d key:%d, want 1/1", groupCount, keyCount)
	}

	different := request
	different.Keys = "K"
	_, err = fixture.service.CreateGroupIdempotent(t.Context(), key, different)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
}

func TestCreateGroupIdempotentCanonicalizesDisabledAliasesAndReplaysNarrowResult(t *testing.T) {
	fixture := newServiceFixture(t)
	request := GroupCreateRequest{
		Name:        stringPointer("canonical-models"),
		UpstreamURL: "https://canonical-models.example.com",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{
			Set: true,
			Values: []GroupModel{{
				ID: "provider-model", Alias: "discarded-a", AliasEnabled: false,
			}},
		},
		Keys: "key-one",
	}
	const key = "238f47a2-9c35-4d6e-8b1a-1234567890ab"

	first, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("first CreateGroupIdempotent() error = %v", err)
	}
	equivalent := request
	equivalent.Models = optionalGroupModels{
		Set: true,
		Values: []GroupModel{{
			ID: "provider-model", Alias: "discarded-b", AliasEnabled: false,
		}},
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), key, equivalent)
	if err != nil {
		t.Fatalf("equivalent replay CreateGroupIdempotent() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("equivalent replay = %#v, first = %#v", replayed, first)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("encode create result: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode create result fields: %v", err)
	}
	if len(fields) != 4 || fields["models"] != nil {
		t.Fatalf("create result fields = %#v, want narrow result", fields)
	}

	var group models.Group
	if err := fixture.db.First(&group, first.GroupID).Error; err != nil {
		t.Fatalf("read created group: %v", err)
	}
	if got := string(group.Config); got != `{}` {
		t.Fatalf("stored config = %s, want empty override", got)
	}
	if stored := loadCreatedGroupModels(t, fixture, first.GroupID); !reflect.DeepEqual(stored, []GroupModel{{ID: "provider-model"}}) {
		t.Fatalf("stored models = %#v, want disabled alias omitted", stored)
	}
}

func TestImportGroupKeysIdempotentReplaysOriginalCountsWithoutSecondInsert(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupForKeyImport(t, fixture, "seed-existing")
	fixture.service.operationRandom = bytes.NewReader(bytes.Repeat([]byte{0x72}, 16))
	request := GroupKeyImportRequest{Keys: " imported \n imported \n"}
	const key = "228f47a2-9c35-4d6e-8b1a-1234567890ab"

	first, err := fixture.service.ImportGroupKeysIdempotent(
		t.Context(),
		key,
		groupID,
		request,
	)
	if err != nil {
		t.Fatalf("first ImportGroupKeysIdempotent() error = %v", err)
	}
	if first.KeysAdded != 1 || first.KeysDuplicated != 1 {
		t.Fatalf("first result = %#v", first)
	}
	replayed, err := fixture.service.ImportGroupKeysIdempotent(
		t.Context(),
		key,
		groupID,
		request,
	)
	if err != nil {
		t.Fatalf("replay ImportGroupKeysIdempotent() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("replay = %#v, first = %#v", replayed, first)
	}
	var count int64
	if err := fixture.db.Model(&models.UpstreamKey{}).
		Where("group_id = ?", groupID).
		Count(&count).Error; err != nil {
		t.Fatalf("count group keys: %v", err)
	}
	if count != 2 {
		t.Fatalf("group key count = %d, want seed + imported = 2", count)
	}

	_, err = fixture.service.ImportGroupKeysIdempotent(
		t.Context(),
		key,
		groupID,
		GroupKeyImportRequest{Keys: "imported"},
	)
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) ||
		apiErr.Code != app_errors.ErrIdempotencyKeyReused.Code {
		t.Fatalf("different multiplicity error = %v", err)
	}
}
