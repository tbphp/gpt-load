package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestEditAccessKeyReplaysNormalizedFields(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "before",
		CostLimitRules: OptionalAccessKeyCostLimitRules{Set: true, Values: []AccessKeyCostLimitRuleRequest{
			{Kind: "total", LimitUSD: "10"},
			{Kind: "periodic", LimitUSD: "2", PeriodSeconds: 3600},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	totalID, periodicID := created.CostLimitRules[0].ID, created.CostLimitRules[1].ID
	totalRule := fmt.Sprintf(`{"id":%d,"kind":"total","limit_usd":"10"}`, totalID)
	periodicRule := fmt.Sprintf(`{"id":%d,"kind":"periodic","limit_usd":"2","period_seconds":3600}`, periodicID)
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d", created.ID)
	for index, test := range []struct {
		name, initial, retry, different string
	}{
		{"name", `"name":" client "`, `"name":"client"`, `"name":"other"`},
		{"default multiplier", `"price_multiplier":"1.0"`, `"price_multiplier":"1"`, `"price_multiplier":"2"`},
		{"fractional multiplier", `"price_multiplier":"1.50"`, `"price_multiplier":"1.5"`, `"price_multiplier":"1.6"`},
		{
			"cost amount",
			fmt.Sprintf(`"cost_limit_rules":[{"id":%d,"kind":"total","limit_usd":"10.00"},%s]`, totalID, periodicRule),
			fmt.Sprintf(`"cost_limit_rules":[%s,%s]`, totalRule, periodicRule),
			fmt.Sprintf(`"cost_limit_rules":[{"id":%d,"kind":"total","limit_usd":"11"},%s]`, totalID, periodicRule),
		},
		{
			"cost rule order and identity",
			fmt.Sprintf(`"cost_limit_rules":[%s,%s]`, totalRule, periodicRule),
			fmt.Sprintf(`"cost_limit_rules":[%s,%s]`, periodicRule, totalRule),
			fmt.Sprintf(`"cost_limit_rules":[{"kind":"total","limit_usd":"10"},%s]`, periodicRule),
		},
		{"empty cost rules", `"cost_limit_rules":[]`, `"cost_limit_rules":[]`, `"cost_limit_rules":[{"kind":"total","limit_usd":"10"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			operationID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 8621+index)
			first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path,
				`{"key":"replacement-value",`+test.initial+`}`, operationID)
			if first.Code != http.StatusOK {
				t.Fatalf("initial edit status = %d", first.Code)
			}
			before := loadAccessKeyRow(t, fixture.db, created.ID)
			replayed := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path,
				`{"key":"replacement-value",`+test.retry+`}`, operationID)
			if replayed.Code != http.StatusOK {
				t.Fatalf("equivalent edit replay status = %d", replayed.Code)
			}
			if !reflect.DeepEqual(decodeAccessKeyLifecycleData(t, first), decodeAccessKeyLifecycleData(t, replayed)) {
				t.Fatal("replay did not return the original result")
			}
			if row := loadAccessKeyRow(t, fixture.db, created.ID); row.KeyValue != before.KeyValue {
				t.Fatal("replay rewrote the credential")
			}
			for _, body := range []string{
				`{"key":"replacement-value"}`,
				`{"key":"replacement-value",` + test.different + `}`,
			} {
				conflict := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, body, operationID)
				if conflict.Code != http.StatusConflict {
					t.Fatalf("omitted or different mutation status = %d", conflict.Code)
				}
			}
		})
	}
}

func TestEditAccessKeyReplaysEquivalentFilters(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupIDs := make([]uint, 0, 2)
	for index := range 2 {
		group, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
			ChannelID: channel.OpenAICompatible, ConnectionType: models.ConnectionTypeAPIKey,
			Params: json.RawMessage(fmt.Sprintf(`{"base_url":"https://group-%d.example.com/v1"}`, index)),
			Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, Credentials: "test-credential",
		})
		if err != nil {
			t.Fatal(err)
		}
		groupIDs = append(groupIDs, group.GroupID)
	}
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "filter-replay"})
	if err != nil {
		t.Fatal(err)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d", created.ID)
	for index, test := range []struct {
		name    string
		initial string
		retry   string
	}{
		{
			name: "normalized sets",
			initial: fmt.Sprintf(`{"key":"replacement-value","filters":{
				"groups":[%d,%d,%d],"protocols":["anthropic","openai-completions","anthropic"],
				"models":[" gpt-b ","gpt-a","gpt-a"],
				"allowed_cidrs":["198.51.100.7/24","192.0.2.1","192.0.2.1/32"]
			}}`, groupIDs[1], groupIDs[0], groupIDs[1]),
			retry: fmt.Sprintf(`{"key":"replacement-value","filters":{
				"groups":[%d,%d],"protocols":["openai-completions","anthropic"],
				"models":["gpt-a","gpt-b"],"allowed_cidrs":["192.0.2.1/32","198.51.100.0/24"]
			}}`, groupIDs[0], groupIDs[1]),
		},
		{
			name:    "empty sets",
			initial: `{"key":"replacement-value","filters":{}}`,
			retry:   `{"key":"replacement-value","filters":{"groups":[],"protocols":[],"models":[],"allowed_cidrs":[]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			operationID := fmt.Sprintf("00000000-0000-4000-8000-%012d", 8430+index)
			first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, test.initial, operationID)
			if first.Code != http.StatusOK {
				t.Fatalf("initial edit status = %d", first.Code)
			}
			before := loadAccessKeyRow(t, fixture.db, created.ID)
			replayed := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, test.retry, operationID)
			if replayed.Code != http.StatusOK {
				t.Fatalf("equivalent filter replay status = %d", replayed.Code)
			}
			if !bytes.Equal(decodeAccessKeyLifecycleData(t, first)["filters"], decodeAccessKeyLifecycleData(t, replayed)["filters"]) {
				t.Fatal("replay did not return the original filter result")
			}
			if row := loadAccessKeyRow(t, fixture.db, created.ID); row.KeyValue != before.KeyValue || !bytes.Equal(row.Filters, before.Filters) {
				t.Fatal("replay rewrote the credential or filters")
			}
			for _, body := range []string{
				`{"key":"replacement-value"}`,
				`{"key":"replacement-value","filters":{"models":["different-model"]}}`,
			} {
				conflict := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, body, operationID)
				if conflict.Code != http.StatusConflict {
					t.Fatalf("omitted or different filters status = %d", conflict.Code)
				}
			}
		})
	}
}

func TestEditAccessKeyReplacesCredentialAtomicallyAndReplays(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "original", Key: "original-credential-value", RPMLimit: OptionalRPMLimit{Set: true, Value: 7},
		CostLimitRules: OptionalAccessKeyCostLimitRules{Set: true, Values: []AccessKeyCostLimitRuleRequest{{Kind: "total", LimitUSD: "10"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := loadAccessKeyRow(t, fixture.db, created.ID)
	ticket, _ := fixture.service.accessQuota.Admit(created.ID, fixture.service.now())
	fixture.service.accessQuota.Complete(ticket, 1234)
	quotaBefore := fixture.service.accessQuota.Snapshot(created.ID, fixture.service.now())
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d", created.ID)
	const firstOperation = "00000000-0000-4000-8000-000000008401"
	const firstBody = `{"name":"changed","key":"x"}`
	first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, firstBody, firstOperation)
	if first.Code != http.StatusOK {
		t.Fatalf("edit with key status = %d", first.Code)
	}
	data := decodeAccessKeyLifecycleData(t, first)
	assertJSONRawEqual(t, data["id"], fmt.Sprint(created.ID))
	assertJSONRawEqual(t, data["name"], `"changed"`)
	assertJSONRawEqual(t, data["masked_key"], `"********"`)
	if _, exists := data["key"]; exists {
		t.Fatal("edit metadata exposed plaintext")
	}
	row := loadAccessKeyRow(t, fixture.db, created.ID)
	if row.KeyHash != fixture.encryption.Hash("x") || row.KeyValue == before.KeyValue || row.RPMLimit != 7 {
		t.Fatal("credential update or policy preservation failed")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[before.KeyHash]; exists {
		t.Fatal("old credential remains active")
	}
	quotaAfter := fixture.service.accessQuota.Snapshot(created.ID, fixture.service.now())
	if len(quotaAfter.Rules) != 1 || quotaAfter.Rules[0].UsedNanoUSD != quotaBefore.Rules[0].UsedNanoUSD || quotaAfter.Rules[0].ID != quotaBefore.Rules[0].ID {
		t.Fatal("editing the key reset usage or quota identity")
	}

	second := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, `{"key":"y"}`, "00000000-0000-4000-8000-000000008402")
	if second.Code != http.StatusOK {
		t.Fatalf("second edit status = %d", second.Code)
	}
	secondRow := loadAccessKeyRow(t, fixture.db, created.ID)
	replayed := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, firstBody, firstOperation)
	if replayed.Code != http.StatusOK {
		t.Fatalf("replay status = %d", replayed.Code)
	}
	if current := loadAccessKeyRow(t, fixture.db, created.ID); current.KeyValue != secondRow.KeyValue {
		t.Fatal("retry overwrote a later credential change")
	}
	conflict := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, `{"name":"changed","key":"z"}`, firstOperation)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed operation body status = %d", conflict.Code)
	}
}

func TestEditAccessKeyEmptyPreservesCredentialAndInvalidInputRollsBack(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "original", Key: "original-key-value"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "other", Key: "duplicate-key-value"}); err != nil {
		t.Fatal(err)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d", created.ID)
	before := loadAccessKeyRow(t, fixture.db, created.ID)
	for _, body := range []string{`{"name":"kept","key":""}`, `{"name":"kept"}`} {
		response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, body, "")
		if response.Code != http.StatusOK {
			t.Fatalf("metadata edit status = %d", response.Code)
		}
		if row := loadAccessKeyRow(t, fixture.db, created.ID); row.KeyValue != before.KeyValue || row.KeyHash != before.KeyHash {
			t.Fatal("empty or omitted key changed the credential")
		}
	}
	for index, test := range []struct {
		key    string
		status int
	}{{"duplicate-key-value", 409}, {"two words", 400}, {authTestKey, 400}} {
		body, err := json.Marshal(map[string]string{"name": "must-rollback", "key": test.key})
		if err != nil {
			t.Fatal(err)
		}
		response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, string(body), fmt.Sprintf("00000000-0000-4000-8000-%012d", 8410+index))
		if response.Code != test.status {
			t.Fatalf("invalid case %d status = %d", index, response.Code)
		}
		if row := loadAccessKeyRow(t, fixture.db, created.ID); row.Name != "kept" || row.KeyHash != before.KeyHash {
			t.Fatal("rejected key change modified metadata")
		}
	}
	missingOperation := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, `{"key":"replacement-value"}`, "")
	if missingOperation.Code != http.StatusPreconditionRequired {
		t.Fatal("key change accepted without an operation identity")
	}
}

func TestEditAccessKeyRecoversCommittedCredentialChange(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "before", Key: "old-credential-value"})
	if err != nil {
		t.Fatal(err)
	}
	before := loadAccessKeyRow(t, fixture.db, created.ID)
	publish := fixture.service.publishSnapshot
	fail := true
	fixture.service.publishSnapshot = func(input state.CompileInput) (*state.ConfigSnapshot, error) {
		if fail {
			return nil, errors.New("injected publish failure")
		}
		return publish(input)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	path := fmt.Sprintf("/api/access-keys/%d", created.ID)
	const operationID = "00000000-0000-4000-8000-000000008421"
	const body = `{"name":"after","key":"new-credential-value"}`
	response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, body, operationID)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed publish status = %d", response.Code)
	}
	committed := loadAccessKeyRow(t, fixture.db, created.ID)
	if committed.KeyHash == before.KeyHash || committed.Name != "after" {
		t.Fatal("transaction was not committed")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[before.KeyHash]; !exists {
		t.Fatal("failed publication replaced the snapshot")
	}
	var operation models.ControlOperation
	if err := fixture.db.Where("idempotency_key = ?", operationID).Take(&operation).Error; err != nil {
		t.Fatal(err)
	}
	if operation.OperationKind != "access_key_update" || bytes.Contains(operation.CanonicalResult, []byte("new-credential-value")) {
		t.Fatal("invalid or secret-bearing operation metadata")
	}
	fail = false
	response = serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, body, operationID)
	if response.Code != http.StatusOK {
		t.Fatalf("recovery status = %d", response.Code)
	}
	if row := loadAccessKeyRow(t, fixture.db, created.ID); row.KeyValue != committed.KeyValue {
		t.Fatal("recovery wrote the credential twice")
	}
	if _, exists := fixture.manager.Current().AccessKeysByHash[committed.KeyHash]; !exists {
		t.Fatal("recovery did not publish the new credential")
	}
}
