package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestCredentialQuotaReserve(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, id := newSubscriptionCredentialFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "reserve-test"}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/credentials/%d", groupID, id)
	update := func(body string, status int) {
		t.Helper()
		res := serveCredentialRequest(t, engine, http.MethodPut, path, body, "reserve-test", "")
		if res.Code != status {
			t.Fatalf("update %s: status=%d body=%s", body, res.Code, res.Body.String())
		}
	}
	candidates := func(registry *state.CredentialRegistry, want int) {
		t.Helper()
		if got := len(registry.CollectCredentialCandidates([]uint{groupID}, nil, time.Now())); got != want {
			t.Fatalf("candidates=%d, want %d", got, want)
		}
	}
	candidates(fixture.registry, 1)
	update(`{"quota_reserve_percent":20}`, http.StatusOK)
	candidates(fixture.registry, 0) // 启用保护后先同步额度，未知时暂停分流。
	resetAt := time.Now().Add(24 * time.Hour)
	for _, sample := range []struct {
		remaining float64
		want      int
	}{{0.21, 1}, {0.2, 0}, {0.1, 0}, {0.8, 1}, {0.2, 0}} {
		fixture.registry.SetCredentialQuotaObservation(id, &sample.remaining, resetAt)
		candidates(fixture.registry, sample.want)
	}
	update(`{"weight_manual":70}`, http.StatusOK)
	candidates(fixture.registry, 0) // 修改其他配置不能清掉额度保护。
	fixture.registry.SetCredentialQuotaObservation(id, nil, time.Time{})
	candidates(fixture.registry, 0) // 同步失败或缺少新额度不能解除已触发的保护。
	fixture.registry.SetCredentialAuthState(id, state.CredentialAuthStateRefreshing)
	fixture.registry.SetCredentialAuthState(id, state.CredentialAuthStateReady)
	candidates(fixture.registry, 0) // 刷新令牌也不能清掉额度保护。
	fixture.registry.ApplyQuotaWindows(id, []providerobservation.QuotaWindow{
		{ID: "weekly", Scope: "account", State: "unknown"},
		{ID: "session", Scope: "account", State: "available", Utilization: new(0.2), ResetAtMS: new(resetAt.UnixMilli())},
	})
	candidates(fixture.registry, 0) // 另一窗口额度充足也不能掩盖未知窗口。
	fixture.registry.SetCredentialQuotaObservation(id, new(0.8), resetAt)
	candidates(fixture.registry, 1)
	fixture.registry.SetCredentialQuotaObservation(id, new(0.2), resetAt)
	item, err := fixture.service.loadCredentialItem(t.Context(), groupID, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(item)
	var presentation struct {
		Percent   int  `json:"quota_reserve_percent"`
		Protected bool `json:"quota_protected"`
	}
	if err := json.Unmarshal(raw, &presentation); err != nil || presentation.Percent != 20 || !presentation.Protected || item.ConfiguredStatus != "active" {
		t.Fatalf("protected presentation=%s, error=%v", raw, err)
	}
	var credential models.Credential
	if err := fixture.db.First(&credential, id).Error; err != nil {
		t.Fatal(err)
	}
	observation := models.CredentialObservation{
		CredentialID: id, IdentityFingerprint: credential.IdentityFingerprint,
		State:        models.CredentialObservationFresh,
		SnapshotJSON: models.JSON(fmt.Sprintf(`{"quota_windows":[{"id":"weekly","scope":"account","utilization":0.8,"reset_at_ms":%d}]}`, resetAt.UnixMilli())),
	}
	if err := fixture.db.Create(&observation).Error; err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		state    models.CredentialObservationState
		snapshot models.JSON
	}{
		{models.CredentialObservationFresh, observation.SnapshotJSON},
		{models.CredentialObservationFresh, models.JSON(`{"quota_windows":[]}`)},
		{models.CredentialObservationError, models.JSON(fmt.Sprintf(`{"quota_windows":[{"id":"weekly","scope":"account","utilization":0.1,"reset_at_ms":%d}]}`, resetAt.UnixMilli()))},
	} {
		if err := fixture.db.Model(&observation).Where("credential_id = ?", id).Updates(map[string]any{"snapshot_json": sample.snapshot, "state": sample.state}).Error; err != nil {
			t.Fatal(err)
		}
		restarted := state.NewCredentialRegistry()
		if err := stateloader.New(fixture.db, state.NewManager(), restarted, fixture.channelRegistry).Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		candidates(restarted, 0) // 已知低额度或未知额度，重启后都不能绕过保护。
	}
	for _, body := range []string{`{"quota_reserve_percent":-1}`, `{"quota_reserve_percent":101}`, `{"quota_reserve_percent":1.5}`, `{"quota_reserve_percent":"20"}`} {
		update(body, http.StatusBadRequest)
	}
	update(`{"quota_reserve_percent":null}`, http.StatusOK)
	candidates(fixture.registry, 1)
}
