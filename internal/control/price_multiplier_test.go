package control

import (
	"bytes"
	"encoding/json"
	"testing"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestPriceMultiplierGroupAndAccessKeyConfiguration(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-price-multiplier")
	settings, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, settings, "1")
	for _, value := range []string{"0", "0.125000", "1000"} {
		var request GroupSettingsUpdateRequest
		if err := json.Unmarshal([]byte(`{"price_multiplier":"`+value+`"}`), &request); err != nil {
			t.Fatal(err)
		}
		updated, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, request)
		if err != nil {
			t.Fatal(err)
		}
		want := value
		if value == "0.125000" {
			want = "0.125"
		}
		assertPriceMultiplierJSON(t, updated, want)
	}
	var create AccessKeyCreateRequest
	if err := json.Unmarshal([]byte(`{"name":"priced key","price_multiplier":"0"}`), &create); err != nil {
		t.Fatal(err)
	}
	key, err := fixture.service.CreateAccessKey(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, key, "0")
	previous := fixture.manager.Current()
	if previous.AccessKeysByID[key.ID].PriceMultiplier != 0 || previous.Groups[groupID].PriceMultiplier != 1_000_000_000 {
		t.Fatal("configured multipliers were not published")
	}
	var update AccessKeyUpdateRequest
	if err := json.Unmarshal([]byte(`{"price_multiplier":"1.250000"}`), &update); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, update)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, updated, "1.25")
	if previous.AccessKeysByID[key.ID].PriceMultiplier != 0 || fixture.manager.Current().AccessKeysByID[key.ID].PriceMultiplier != 1_250_000 {
		t.Fatal("snapshot publication changed historical multiplier")
	}
	collection, err := fixture.service.ListAccessKeyCollection(t.Context(), AccessKeyCollectionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 {
		t.Fatalf("collection = %#v", collection)
	}
	assertPriceMultiplierJSON(t, collection.Items[0], "1.25")
	name := "renamed key"
	preserved, err := fixture.service.UpdateAccessKey(t.Context(), key.ID, AccessKeyUpdateRequest{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, preserved, "1.25")
	group, err := fixture.service.GetGroupSummary(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, group, "1000")
	preservedGroup, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Name: optionalField[string]{Set: true, Value: "renamed multiplier group"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, preservedGroup, "1000")
	home, err := fixture.service.ReadAccessKeyHomeBase(t.Context(), fixture.service.now().UnixMilli(), key.ID)
	if err != nil || home.CurrentAccessKey == nil {
		t.Fatalf("access key home = %#v, %v", home, err)
	}
	assertPriceMultiplierJSON(t, home.CurrentAccessKey, "1.25")
	rotated, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), "218f47a2-9c35-4d6e-8b1a-123456789011", key.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, rotated, "1.25")
	if fixture.manager.Current().AccessKeysByID[key.ID].PriceMultiplier != 1_250_000 {
		t.Fatal("rotation changed the configured price multiplier")
	}
}

func TestPriceMultiplierLegacyAccessKeyResultsReplayOriginalDefault(t *testing.T) {
	for _, operation := range []string{"create", "rotate"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newServiceFixture(t)
			request := AccessKeyCreateRequest{Name: "legacy multiplier"}
			const idempotencyKey = "218f47a2-9c35-4d6e-8b1a-123456789012"
			var id uint
			if operation == "create" {
				created, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), idempotencyKey, request)
				if err != nil {
					t.Fatal(err)
				}
				id = created.ID
			} else {
				created, err := fixture.service.CreateAccessKey(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				id = created.ID
				if _, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), idempotencyKey, id); err != nil {
					t.Fatal(err)
				}
			}
			var row models.ControlOperation
			if err := fixture.db.Where("idempotency_key = ?", idempotencyKey).Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			var legacyResult map[string]json.RawMessage
			if err := json.Unmarshal(row.CanonicalResult, &legacyResult); err != nil {
				t.Fatal(err)
			}
			delete(legacyResult, "price_multiplier")
			encoded, err := json.Marshal(legacyResult)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Model(&row).Update("canonical_result", models.JSON(encoded)).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.service.UpdateAccessKey(t.Context(), id, AccessKeyUpdateRequest{
				PriceMultiplier: optionalField[string]{Set: true, Value: "0.5"},
			}); err != nil {
				t.Fatal(err)
			}
			if operation == "create" {
				request.PriceMultiplier = optionalField[string]{Set: true, Value: "1.000000"}
				replayed, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), idempotencyKey, request)
				if err != nil || !replayed.Replayed || replayed.Key != "" {
					t.Fatalf("legacy create replay = %#v, %v", replayed, err)
				}
				assertPriceMultiplierJSON(t, replayed, "1")
			} else {
				replayed, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), idempotencyKey, id)
				if err != nil || !replayed.Replayed || replayed.Key != "" {
					t.Fatalf("legacy rotate replay = %#v, %v", replayed, err)
				}
				assertPriceMultiplierJSON(t, replayed, "1")
			}
			if fixture.manager.Current().AccessKeysByID[id].PriceMultiplier != 500_000 {
				t.Fatal("replay changed the current price multiplier")
			}
		})
	}
}

func TestPriceMultiplierGroupDefaultPreservesLegacyDigest(t *testing.T) {
	fixture := newServiceFixture(t)
	request := GroupCreateRequest{
		Name: stringPointer("legacy multiplier"), ChannelID: channel.OpenAI, ConnectionType: "api_key",
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true}, Credentials: "K",
	}
	const idempotencyKey = "218f47a2-9c35-4d6e-8b1a-123456789013"
	created, err := fixture.service.CreateGroupIdempotent(t.Context(), idempotencyKey, request)
	if err != nil {
		t.Fatal(err)
	}
	legacyDigest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindGroupCreate,
		PathTemplate: "/api/groups", ResourceLocator: "new", AuthScopeID: idempotencyAuthScopeID,
		CanonicalBody: []byte(`{"channel_id":"openai","connection_type":"api_key","credentials":["K"],"models":null,"name":"legacy multiplier","params":{}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var row models.ControlOperation
	if err := fixture.db.Where("idempotency_key = ?", idempotencyKey).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(row.RequestDigest, legacyDigest.Digest[:]) {
		t.Fatal("default multiplier changed the pre-multiplier group create digest")
	}
	request.PriceMultiplier = optionalField[string]{Set: true, Value: "1.000000"}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), idempotencyKey, request)
	if err != nil || replayed != created {
		t.Fatalf("default-equivalent replay = %#v, %v", replayed, err)
	}
}

func TestPriceMultiplierCreationAndIdempotency(t *testing.T) {
	fixture := newServiceFixture(t)
	request := GroupCreateRequest{
		Name: stringPointer("multiplier-group"), ChannelID: channel.OpenAICompatible,
		ConnectionType: "api_key", Params: json.RawMessage(`{"base_url":"https://multiplier.example/v1"}`),
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{ID: "model"}}}, Credentials: "sk-multiplier",
		PriceMultiplier: optionalField[string]{Set: true, Value: "0"},
	}
	created, err := fixture.service.CreateGroupIdempotent(t.Context(), "218f47a2-9c35-4d6e-8b1a-123456789008", request)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), created.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, settings, "0")
	request.PriceMultiplier.Value = "0.000000"
	if _, err := fixture.service.CreateGroupIdempotent(t.Context(), "218f47a2-9c35-4d6e-8b1a-123456789008", request); err != nil {
		t.Fatal(err)
	}
	request.PriceMultiplier.Value = "1"
	_, err = fixture.service.CreateGroupIdempotent(t.Context(), "218f47a2-9c35-4d6e-8b1a-123456789008", request)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
	keyRequest := AccessKeyCreateRequest{Name: "idempotent multiplier"}
	const key = "218f47a2-9c35-4d6e-8b1a-123456789009"
	first, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), key, keyRequest)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, first, "1")
	keyRequest.PriceMultiplier = optionalField[string]{Set: true, Value: "1.000000"}
	replayed, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), key, keyRequest)
	if err != nil || !replayed.Replayed {
		t.Fatalf("default replay = %#v, %v", replayed, err)
	}
	keyRequest.PriceMultiplier.Value = "0.5"
	_, err = fixture.service.CreateAccessKeyIdempotent(t.Context(), key, keyRequest)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
	keyRequest.PriceMultiplier.Value = "0"
	zero, err := fixture.service.CreateAccessKeyIdempotent(t.Context(), "218f47a2-9c35-4d6e-8b1a-123456789010", keyRequest)
	if err != nil {
		t.Fatal(err)
	}
	assertPriceMultiplierJSON(t, zero, "0")
	if fixture.manager.Current().AccessKeysByID[zero.ID].PriceMultiplier != pricing.PriceMultiplier(0) {
		t.Fatal("idempotent create lost zero multiplier")
	}
}

func TestPriceMultiplierConfigurationRejectsInvalidValues(t *testing.T) {
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "sk-price-invalid")
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "invalid multiplier"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `""`, `"-1"`, `"1000.000001"`, `"0.1234567"`, `"1e2"`, `1`, `true`} {
		t.Run(raw, func(t *testing.T) {
			var groupRequest GroupSettingsUpdateRequest
			if err := json.Unmarshal([]byte(`{"price_multiplier":`+raw+`}`), &groupRequest); err == nil {
				_, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, groupRequest)
				assertAPIErrorCode(t, err, app_errors.ErrValidation.Code)
			}
			var keyRequest AccessKeyUpdateRequest
			if err := json.Unmarshal([]byte(`{"price_multiplier":`+raw+`}`), &keyRequest); err == nil {
				_, err = fixture.service.UpdateAccessKey(t.Context(), key.ID, keyRequest)
				assertAPIErrorCode(t, err, app_errors.ErrValidation.Code)
			}
			var createRequest AccessKeyCreateRequest
			if err := json.Unmarshal([]byte(`{"name":"bad multiplier","price_multiplier":`+raw+`}`), &createRequest); err == nil {
				_, err = fixture.service.CreateAccessKey(t.Context(), createRequest)
				assertAPIErrorCode(t, err, app_errors.ErrValidation.Code)
			}
			var groupCreate GroupCreateRequest
			if err := json.Unmarshal([]byte(`{"name":"bad multiplier group","channel_id":"openai","connection_type":"api_key","params":{},"models":[],"credentials":"sk-invalid","price_multiplier":`+raw+`}`), &groupCreate); err == nil {
				_, err = fixture.service.CreateGroup(t.Context(), groupCreate)
				assertAPIErrorCode(t, err, app_errors.ErrValidation.Code)
			}
		})
	}
}

func assertPriceMultiplierJSON(t *testing.T, value any, want string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body["price_multiplier"] != want {
		t.Fatalf("price_multiplier = %#v, want %q", body["price_multiplier"], want)
	}
}
