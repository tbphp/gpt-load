package loader_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

// codexLegacyTestCredential 构造一份 Codex 凭据：id_token 里同时带 workspace 与 user，
// 这正是「同一 workspace 下不同用户」的形态。
func codexLegacyTestCredential(t *testing.T, accountID, userID, refreshToken string) string {
	t.Helper()
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	idToken := encode(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		encode(map[string]any{
			"https://api.openai.com/auth": map[string]string{
				"chatgpt_account_id": accountID,
				"chatgpt_user_id":    userID,
			},
		}) + ".signature"
	raw, err := json.Marshal(map[string]string{
		"type":          "codex",
		"access_token":  "access-" + userID,
		"refresh_token": refreshToken,
		"account_id":    accountID,
		"email":         userID + "@example.com",
		"id_token":      idToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 升级到「身份含 user 维度」的版本后，既有行的 identity_fingerprint 必须在加载期被
// 重写为当前身份定义计算出的值：
//   - 凭据数据本身的 fingerprint 不能变（canonical 形状是它的契约）；
//   - 迁移必须幂等，第二次加载不得再写库。
func TestLoadMigratesCredentialIdentityFingerprintFromWorkspaceOnly(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "codex-identity-migration", ChannelID: string(channel.Codex),
		ConnectionType: models.ConnectionTypeSubscription,
		Params:         models.JSON(`{}`), Models: models.JSON(`[]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)

	service, err := encryption.NewService("identity-migration-master-key")
	if err != nil {
		t.Fatal(err)
	}
	const (
		workspace = "70a31b69-2c1a-4469-9e7c-3935a96e3865"
		userID    = "user-AAAA"
	)
	credential := codexLegacyTestCredential(t, workspace, userID, "refresh-legacy")
	channels, subscriptions := testSubscriptionRuntime(t)
	// 数据指纹是 canonical 之后的哈希，这里必须先规范化再算，才能模拟旧版本写下的行。
	canonical, err := subscriptions.CanonicalCredential(channel.Codex, []byte(credential))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := service.Encrypt(credential)
	if err != nil {
		t.Fatal(err)
	}
	// 旧版本的身份只取 workspace，因此旧指纹是按纯 workspace 身份算出来的。
	legacyIdentity := workspace
	legacyFingerprint := service.Hash("credential-identity/v1|codex|codex|" + legacyIdentity)
	if legacyFingerprint == "" {
		t.Fatal("legacy fingerprint is empty")
	}
	row := models.Credential{
		GroupID: group.ID, Data: ciphertext,
		Fingerprint:         service.Hash(string(canonical)),
		IdentityFingerprint: legacyFingerprint,
		SecretVersion:       1,
		AuthState:           models.CredentialAuthStateReady,
		Status:              models.CredentialStatusActive,
	}
	mustCreate(t, db, &row)

	if err := stateloader.NewWithCredentialValidation(
		db, state.NewManager(), state.NewCredentialRegistry(), channels, subscriptions, service,
	).Load(t.Context()); err != nil {
		t.Fatalf("Load() with legacy identity fingerprint = %v, want success", err)
	}

	var migrated models.Credential
	if err := db.First(&migrated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	wantFingerprint := service.Hash("credential-identity/v1|codex|codex|" + workspace + "/" + userID)
	if migrated.IdentityFingerprint != wantFingerprint {
		t.Fatalf("identity fingerprint = %q, want %q", migrated.IdentityFingerprint, wantFingerprint)
	}
	// canonical 形状未变 -> 数据指纹必须原样保留，否则既有凭据会全部校验失败。
	if migrated.Fingerprint != row.Fingerprint {
		t.Fatalf("credential fingerprint changed: %q -> %q", row.Fingerprint, migrated.Fingerprint)
	}
	if migrated.SecretVersion != row.SecretVersion {
		t.Fatalf("secret version changed: %d -> %d", row.SecretVersion, migrated.SecretVersion)
	}

	// 幂等：第二次加载不应再产生差异，也不应报错。
	if err := stateloader.NewWithCredentialValidation(
		db, state.NewManager(), state.NewCredentialRegistry(), channels, subscriptions, service,
	).Load(t.Context()); err != nil {
		t.Fatalf("second Load() = %v, want success", err)
	}
	var afterSecond models.Credential
	if err := db.First(&afterSecond, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterSecond.IdentityFingerprint != wantFingerprint {
		t.Fatalf("second load changed identity fingerprint to %q", afterSecond.IdentityFingerprint)
	}
}

// 令牌里没有 user claim 的凭据必须保持旧指纹不变，避免升级时无谓地要求重新授权。
func TestLoadKeepsWorkspaceIdentityWhenUserClaimIsAbsent(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "codex-identity-no-user", ChannelID: string(channel.Codex),
		ConnectionType: models.ConnectionTypeSubscription,
		Params:         models.JSON(`{}`), Models: models.JSON(`[]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)

	service, err := encryption.NewService("identity-no-user-master-key")
	if err != nil {
		t.Fatal(err)
	}
	const workspace = "770d8f80-b7ad-4062-af19-a0d45a5dc62a"
	credential := `{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"` + workspace + `"}`
	channels, subscriptions := testSubscriptionRuntime(t)
	canonical, err := subscriptions.CanonicalCredential(channel.Codex, []byte(credential))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := service.Encrypt(credential)
	if err != nil {
		t.Fatal(err)
	}
	legacyFingerprint := service.Hash("credential-identity/v1|codex|codex|" + workspace)
	row := models.Credential{
		GroupID: group.ID, Data: ciphertext,
		Fingerprint:         service.Hash(string(canonical)),
		IdentityFingerprint: legacyFingerprint,
		SecretVersion:       1,
		AuthState:           models.CredentialAuthStateReady,
		Status:              models.CredentialStatusActive,
	}
	mustCreate(t, db, &row)

	if err := stateloader.NewWithCredentialValidation(
		db, state.NewManager(), state.NewCredentialRegistry(), channels, subscriptions, service,
	).Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	var migrated models.Credential
	if err := db.First(&migrated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.IdentityFingerprint != legacyFingerprint {
		t.Fatalf("identity fingerprint changed without a user claim: %q -> %q",
			legacyFingerprint, migrated.IdentityFingerprint)
	}
}

// 迁移只应在「凭据数据指纹校验通过」之后发生：数据被改动的行必须照旧拒绝启动。
func TestLoadRejectsTamperedCredentialBeforeIdentityMigration(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "codex-identity-tampered", ChannelID: string(channel.Codex),
		ConnectionType: models.ConnectionTypeSubscription,
		Params:         models.JSON(`{}`), Models: models.JSON(`[]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	service, err := encryption.NewService("identity-tampered-master-key")
	if err != nil {
		t.Fatal(err)
	}
	credential := codexLegacyTestCredential(t, "ws", "user-AAAA", "refresh-legacy")
	ciphertext, err := service.Encrypt(credential)
	if err != nil {
		t.Fatal(err)
	}
	row := models.Credential{
		GroupID: group.ID, Data: ciphertext,
		Fingerprint:         service.Hash("a-different-canonical-shape"),
		IdentityFingerprint: service.Hash("credential-identity/v1|codex|codex|ws"),
		SecretVersion:       1,
		AuthState:           models.CredentialAuthStateReady,
		Status:              models.CredentialStatusActive,
	}
	mustCreate(t, db, &row)

	channels, subscriptions := testSubscriptionRuntime(t)
	err = stateloader.NewWithCredentialValidation(
		db, state.NewManager(), state.NewCredentialRegistry(), channels, subscriptions, service,
	).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("Load() = %v, want credential fingerprint mismatch", err)
	}
}
