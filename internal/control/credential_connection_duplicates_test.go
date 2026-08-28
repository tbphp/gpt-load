package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestCreateSubscriptionGroupSkipsDuplicateReadyStageIdentity(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	first := mustImportSubscriptionStage(t, fixture, "create-duplicate", "first@example.com")
	second := mustImportSubscriptionStage(t, fixture, "create-duplicate", "second@example.com")

	created, err := fixture.service.CreateGroupIdempotent(
		t.Context(),
		"00000000-0000-4000-8000-00000000d101",
		GroupCreateRequest{
			Name: stringPointer("subscription duplicate create"), ChannelID: channel.Codex,
			ConnectionType:      models.ConnectionTypeSubscription,
			Models:              optionalGroupModels{Set: true},
			StagedCredentialIDs: []string{first.StageID, second.StageID},
		},
	)
	if err != nil {
		t.Fatalf("CreateGroupIdempotent() error = %v", err)
	}
	if created.CredentialsAdded != 1 || created.CredentialsDuplicated != 1 {
		t.Fatalf("created = %#v", created)
	}
}

func TestConnectSubscriptionGroupSkipsExistingAndBatchDuplicateStages(t *testing.T) {
	t.Parallel()
	fixture, groupID, _ := newSubscriptionCredentialFixture(t)
	existing := mustImportSubscriptionStage(t, fixture, "account-observation", "existing@example.com")
	newFirst := mustImportSubscriptionStage(t, fixture, "connect-new", "new-first@example.com")
	newSecond := mustImportSubscriptionStage(t, fixture, "connect-new", "new-second@example.com")

	result, err := fixture.service.ConnectGroupCredentialsIdempotent(
		t.Context(),
		"00000000-0000-4000-8000-00000000d102",
		groupID,
		[]string{existing.StageID, newFirst.StageID, newSecond.StageID},
	)
	if err != nil {
		t.Fatalf("ConnectGroupCredentialsIdempotent() error = %v", err)
	}
	if result.CredentialsAdded != 1 || result.CredentialsDuplicated != 2 {
		t.Fatalf("result = %#v", result)
	}
	var credentialCount int64
	if err := fixture.db.Model(&models.Credential{}).
		Where("group_id = ?", groupID).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 2 {
		t.Fatalf("credential count = %d, want 2", credentialCount)
	}
	var consumedCount int64
	if err := fixture.db.Model(&models.CredentialStage{}).
		Where("id IN ? AND status = ?", []string{existing.StageID, newFirst.StageID, newSecond.StageID}, models.CredentialStageConsumed).
		Count(&consumedCount).Error; err != nil {
		t.Fatal(err)
	}
	if consumedCount != 3 {
		t.Fatalf("consumed stage count = %d, want 3", consumedCount)
	}
}

func TestConnectSubscriptionGroupReplacesCredentialThatNeedsReauthorization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		authState      models.CredentialAuthState
		authErrorCode  string
		idempotencyKey string
	}{
		{
			name: "reauthorization required", authState: models.CredentialAuthStateReauthorizationRequired,
			authErrorCode: "refresh_rejected", idempotencyKey: "00000000-0000-4000-8000-00000000d103",
		},
		{
			name: "outcome unknown", authState: models.CredentialAuthStateOutcomeUnknown,
			authErrorCode: "refresh_outcome_unknown", idempotencyKey: "00000000-0000-4000-8000-00000000d104",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			var before models.Credential
			if err := fixture.db.First(&before, credentialID).Error; err != nil {
				t.Fatal(err)
			}
			const weight = 37
			if err := fixture.db.Model(&models.Credential{}).Where("id = ?", credentialID).Updates(map[string]any{
				"auth_state": test.authState, "auth_error_code": test.authErrorCode,
				"status": models.CredentialStatusDisabled, "weight_manual": weight,
			}).Error; err != nil {
				t.Fatal(err)
			}
			stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(fmt.Sprintf(
				`{"type":"codex","access_token":"replacement-access-%s","refresh_token":"replacement-refresh-%s","account_id":"account-observation","email":%q}`,
				test.authState,
				test.authState,
				fmt.Sprintf("replacement-%s@example.com", test.authState),
			)))
			if err != nil {
				t.Fatal(err)
			}

			inspection, err := fixture.service.InspectGroupCredentialConnection(
				t.Context(), groupID, []string{stage.StageID},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(inspection.DuplicatedStageIDs) != 0 {
				t.Errorf("inspection duplicates = %v, want none", inspection.DuplicatedStageIDs)
			}

			result, err := fixture.service.ConnectGroupCredentialsIdempotent(
				t.Context(), test.idempotencyKey, groupID, []string{stage.StageID},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.CredentialsAdded != 1 || result.CredentialsDuplicated != 0 {
				t.Errorf("result = %#v", result)
			}
			var after models.Credential
			if err := fixture.db.First(&after, credentialID).Error; err != nil {
				t.Fatal(err)
			}
			if after.Fingerprint == before.Fingerprint || after.Data == before.Data ||
				after.IdentityFingerprint != before.IdentityFingerprint ||
				after.SecretVersion != before.SecretVersion+1 ||
				after.AuthState != models.CredentialAuthStateReady || after.AuthErrorCode != "" {
				t.Errorf(
					"replacement state = fingerprint_changed:%t data_changed:%t identity_preserved:%t version:%d auth:%q error:%q",
					after.Fingerprint != before.Fingerprint,
					after.Data != before.Data,
					after.IdentityFingerprint == before.IdentityFingerprint,
					after.SecretVersion,
					after.AuthState,
					after.AuthErrorCode,
				)
			}
			if after.Status != models.CredentialStatusDisabled ||
				after.WeightManual == nil || *after.WeightManual != weight {
				t.Errorf("operator settings changed: status=%q weight=%v", after.Status, after.WeightManual)
			}
			var credentialCount int64
			if err := fixture.db.Model(&models.Credential{}).
				Where("group_id = ?", groupID).Count(&credentialCount).Error; err != nil {
				t.Fatal(err)
			}
			if credentialCount != 1 {
				t.Errorf("credential count = %d, want 1", credentialCount)
			}
			var consumed models.CredentialStage
			if err := fixture.db.Take(&consumed, "id = ?", stage.StageID).Error; err != nil {
				t.Fatal(err)
			}
			if consumed.Status != models.CredentialStageConsumed || consumed.EncryptedPayload != "" {
				t.Errorf("stage was not consumed after replacement: %#v", consumed)
			}
		})
	}
}

func TestInspectSubscriptionConnectionIdentifiesExactDuplicateStages(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture, groupID, _ := newSubscriptionCredentialFixture(t)
	existing := mustImportSubscriptionStage(t, fixture, "account-observation", "existing@example.com")
	newFirst := mustImportSubscriptionStage(t, fixture, "inspect-new", "new-first@example.com")
	newSecond := mustImportSubscriptionStage(t, fixture, "inspect-new", "new-second@example.com")
	stageIDs := []string{existing.StageID, newFirst.StageID, newSecond.StageID}

	engine := gin.New()
	const auth = "credential-duplicate-inspection-auth"
	NewServer(&config.Config{AuthKey: auth}, fixture.service).RegisterRoutes(engine)
	encoded, err := json.Marshal(CredentialConnectRequest{StagedCredentialIDs: stageIDs})
	if err != nil {
		t.Fatal(err)
	}
	response := serveCredentialRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/connect/inspect", groupID),
		string(encoded),
		auth,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("inspection = %d %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			DuplicatedStageIDs []string `json:"duplicated_stage_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	want := []string{existing.StageID}
	if newFirst.StageID < newSecond.StageID {
		want = append(want, newSecond.StageID)
	} else {
		want = append(want, newFirst.StageID)
	}
	sort.Strings(want)
	sort.Strings(envelope.Data.DuplicatedStageIDs)
	if envelope.Code != 0 || !reflect.DeepEqual(envelope.Data.DuplicatedStageIDs, want) {
		t.Fatalf("inspection = %#v, want duplicate stages %#v", envelope, want)
	}
}
