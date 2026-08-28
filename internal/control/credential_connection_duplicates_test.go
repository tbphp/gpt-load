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
