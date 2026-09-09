package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/storage/models"
)

func TestCustomAccessKeyCreationAndReplay(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	for _, key := range []string{"x", "123456", "imported-key_Z9-", "symbols-!\"\\'~", strings.Repeat("A", 256)} {
		t.Run(fmt.Sprintf("length-%d", len(key)), func(t *testing.T) {
			fixture := newServiceFixture(t)
			engine := newAccessKeyLifecycleEngine(t, fixture)
			body, err := json.Marshal(map[string]string{"name": "custom", "key": key})
			if err != nil {
				t.Fatal(err)
			}
			const operation = "00000000-0000-4000-8000-000000008101"
			first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", string(body), operation)
			if first.Code != http.StatusOK {
				t.Fatalf("custom key creation status = %d", first.Code)
			}
			var response struct {
				Data AccessKeyCreateResult `json:"data"`
			}
			if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			created := response.Data
			if created.Key != key || created.ID == 0 {
				t.Fatal("creation did not preserve the custom key")
			}
			if len(key) <= 8 && created.MaskedKey != "********" {
				t.Fatal("short key was not fully masked")
			}
			if len(key) > 8 && len(key) <= 16 && created.MaskedKey != "****"+key[len(key)-4:] {
				t.Fatal("custom key suffix was not preserved")
			}
			if len(key) > 16 && created.MaskedKey != key[:6]+"****"+key[len(key)-4:] {
				t.Fatal("custom key prefix and suffix were not preserved")
			}
			row := loadAccessKeyRow(t, fixture.db, created.ID)
			if row.KeyValue == key || row.KeyHash != fixture.encryption.Hash(key) {
				t.Fatal("custom key storage is not encrypted and hashed")
			}
			revealed, err := fixture.service.RevealAccessKey(t.Context(), created.ID)
			if err != nil || revealed.Key != key {
				t.Fatal("custom key reveal failed")
			}
			if _, ok := fixture.manager.Current().AccessKeysByHash[row.KeyHash]; !ok {
				t.Fatal("custom key not published")
			}
			request := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			login := httptest.NewRecorder()
			engine.ServeHTTP(login, request)
			if login.Code != http.StatusOK {
				t.Fatalf("custom key login status = %d", login.Code)
			}
			replay := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", string(body), operation)
			if replay.Code != http.StatusOK {
				t.Fatalf("replay status = %d", replay.Code)
			}
			data := decodeAccessKeyLifecycleData(t, replay)
			if _, exists := data["key"]; exists {
				t.Fatal("replay exposed a secret")
			}
			assertJSONRawEqual(t, data["replayed"], "true")
			otherBody, err := json.Marshal(map[string]string{"name": "custom", "key": "different-custom-key"})
			if err != nil {
				t.Fatal(err)
			}
			conflict := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", string(otherBody), operation)
			if conflict.Code != http.StatusConflict {
				t.Fatalf("changed key replay status = %d", conflict.Code)
			}
			duplicate := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", string(body), "00000000-0000-4000-8000-000000008102")
			if duplicate.Code != http.StatusConflict {
				t.Fatalf("duplicate key status = %d", duplicate.Code)
			}
			var count int64
			if err := fixture.db.Model(&models.AccessKey{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatal("replay or duplicate created another key")
			}
			var operations []models.ControlOperation
			if err := fixture.db.Find(&operations).Error; err != nil {
				t.Fatal(err)
			}
			for _, operation := range operations {
				var metadata map[string]json.RawMessage
				if len(operation.CanonicalResult) > 0 {
					if err := json.Unmarshal(operation.CanonicalResult, &metadata); err != nil {
						t.Fatal(err)
					}
					if _, exists := metadata["key"]; exists {
						t.Fatal("operation metadata persisted the secret")
					}
				}
			}
		})
	}
}

func TestCustomAccessKeyEmptyUsesAutomaticGeneration(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := newAccessKeyLifecycleEngine(t, fixture)
	const operation = "00000000-0000-4000-8000-000000008201"
	response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", `{"name":"automatic","key":""}`, operation)
	if response.Code != http.StatusOK {
		t.Fatalf("empty key creation status = %d", response.Code)
	}
	data := decodeAccessKeyLifecycleData(t, response)
	var key string
	if err := json.Unmarshal(data["key"], &key); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "sk-gl-") || len(key) != 38 {
		t.Fatal("empty input did not generate a random key")
	}
	replay := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", `{"name":"automatic"}`, operation)
	if replay.Code != http.StatusOK {
		t.Fatalf("omitted key replay status = %d", replay.Code)
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, replay)["replayed"], "true")
}

func TestCustomAccessKeyRejectsUnusableValues(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := newAccessKeyLifecycleEngine(t, fixture)
	for index, key := range []string{" ", "leading ", "two words", "line\nbreak", "tab\tkey", "nul\x00key", "中文", strings.Repeat("x", 257), authTestKey} {
		body, err := json.Marshal(map[string]string{"name": "invalid", "key": key})
		if err != nil {
			t.Fatal(err)
		}
		response := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", string(body), fmt.Sprintf("00000000-0000-4000-8000-%012d", index+8200))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("case %d status = %d", index, response.Code)
		}
		var envelope struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		wantCode := "INVALID_CUSTOM_ACCESS_KEY"
		if key == authTestKey {
			wantCode = "ACCESS_KEY_ADMIN_CONFLICT"
		}
		if envelope.Code != wantCode {
			t.Fatalf("case %d error code = %s", index, envelope.Code)
		}
	}
	var count int64
	if err := fixture.db.Model(&models.AccessKey{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("invalid input created a key")
	}
}
