package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func subscriptionIdentityJSON(t *testing.T, channelID channel.ID, account, scope, token, expires string) []byte {
	t.Helper()
	values := map[string]string{"type": string(channelID), "access_token": "access-" + token, "refresh_token": "refresh-" + token, "email": token + "@example.com", "expired": expires}
	if channelID == channel.Codex {
		values["account_id"] = account
		if scope != "" {
			claims, err := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account, "chatgpt_user_id": scope}})
			if err != nil {
				t.Fatal(err)
			}
			values["id_token"] = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
		}
	} else {
		values["account_uuid"] = account
		if scope != "" {
			values["organization_uuid"] = scope
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSubscriptionConnectionUsesCompleteIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		channel        channel.ID
		account, scope string
		added          int
	}{
		{"codex different users", channel.Codex, "account-one", "scope-two", 1},
		{"codex different workspaces", channel.Codex, "account-two", "scope-one", 1},
		{"codex reauthorization", channel.Codex, "account-one", "scope-one", 0},
		{"claude different organizations", channel.Claude, "account-one", "scope-two", 1},
		{"claude different users", channel.Claude, "account-two", "scope-one", 1},
		{"claude reauthorization", channel.Claude, "account-one", "scope-one", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, authState := range []models.CredentialAuthState{models.CredentialAuthStateReady, models.CredentialAuthStateReauthorizationRequired} {
				t.Run(string(authState), func(t *testing.T) {
					f := newServiceFixture(t)
					importStage := func(account, scope, token string) CredentialStageResult {
						result, err := f.service.ImportCredentialStage(t.Context(), test.channel, subscriptionIdentityJSON(t, test.channel, account, scope, token, "2099-01-01T00:00:00Z"))
						if err != nil {
							t.Fatal(err)
						}
						return result
					}
					first := importStage("account-one", "scope-one", "first")
					group, err := f.service.CreateGroup(t.Context(), GroupCreateRequest{Name: stringPointer("identity"), ChannelID: test.channel, ConnectionType: models.ConnectionTypeSubscription, Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, StagedCredentialIDs: []string{first.StageID}})
					if err != nil {
						t.Fatal(err)
					}
					var original models.Credential
					if err := f.db.Where("group_id = ?", group.GroupID).Take(&original).Error; err != nil {
						t.Fatal(err)
					}
					if err := f.db.Model(&original).Update("auth_state", authState).Error; err != nil {
						t.Fatal(err)
					}
					second := importStage(test.account, test.scope, "second")
					result, err := f.service.ConnectGroupCredentials(t.Context(), group.GroupID, []string{second.StageID})
					if err != nil {
						t.Fatal(err)
					}
					wantAdded := test.added
					if authState == models.CredentialAuthStateReauthorizationRequired {
						wantAdded = 1
					}
					if result.CredentialsAdded != wantAdded || result.CredentialsDuplicated != 1-wantAdded {
						t.Fatalf("added/duplicated = %d/%d, want %d/%d", result.CredentialsAdded, result.CredentialsDuplicated, wantAdded, 1-wantAdded)
					}
					var rows []models.Credential
					if err := f.db.Where("group_id = ?", group.GroupID).Order("id ASC").Find(&rows).Error; err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1+test.added {
						t.Fatalf("credential count = %d, want %d", len(rows), 1+test.added)
					}
					if test.added == 1 && rows[0].Data != original.Data {
						t.Fatal("another identity replaced the existing credential")
					}
				})
			}
		})
	}
}

func TestSubscriptionRefreshIdentityAllowsEnrichmentWithoutDowngrade(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.Codex, channel.Claude} {
		t.Run(string(channelID), func(t *testing.T) {
			for _, test := range []struct {
				name, oldScope, newAccount, newScope string
				allowed                              bool
			}{
				{"same identity", "scope-one", "account-one", "scope-one", true},
				{"legacy metadata still absent", "", "account-one", "", true},
				{"enriched metadata", "", "account-one", "scope-one", true},
				{"missing metadata", "scope-one", "account-one", "", false},
				{"different scope", "scope-one", "account-one", "scope-two", false},
				{"different account", "scope-one", "account-two", "scope-one", false},
			} {
				t.Run(test.name, func(t *testing.T) {
					for _, path := range []string{"transient", "ready stage"} {
						t.Run(path, func(t *testing.T) {
							f := newServiceFixture(t)
							driver, ok := subscriptionsDriver(f.service.subscriptions, channelID)
							if !ok {
								t.Fatal("driver missing")
							}
							current, err := driver.Parse(subscriptionIdentityJSON(t, channelID, "account-one", test.oldScope, "current", "2000-01-01T00:00:00Z"))
							if err != nil {
								t.Fatal(err)
							}
							refreshed, err := driver.Parse(subscriptionIdentityJSON(t, channelID, test.newAccount, test.newScope, "next", "2099-01-01T00:00:00Z"))
							if err != nil {
								t.Fatal(err)
							}
							f.service.refreshSubscriptionCredential = func(context.Context, channel.ID, subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
								return refreshed, nil
							}
							if path == "transient" {
								_, err = f.service.prepareTransientSubscriptionCredential(t.Context(), channelID, driver, current)
							} else {
								stage, stageErr := f.service.persistReadyCredentialStage(t.Context(), channelID, "oauth_file", current)
								if stageErr != nil {
									t.Fatal(stageErr)
								}
								row, loadErr := f.service.loadCredentialStage(t.Context(), stage.StageID)
								if loadErr != nil {
									t.Fatal(loadErr)
								}
								_, err = f.service.prepareReadySubscriptionStageCredential(t.Context(), row, driver, current, true)
								if test.allowed && err == nil {
									// 正常刷新后的短期暂存必须仍可消费，不能留下过期的身份指纹。
									_, err = f.service.CreateGroup(t.Context(), GroupCreateRequest{Name: stringPointer("refreshed identity"), ChannelID: channelID, ConnectionType: models.ConnectionTypeSubscription, Models: optionalGroupModels{Set: true, Values: []GroupModel{}}, StagedCredentialIDs: []string{stage.StageID}})
								}
							}
							if test.allowed && err != nil {
								t.Fatalf("same authorization with optional metadata change rejected: %v", err)
							}
							if !test.allowed && !errors.Is(err, app_errors.ErrCredentialReauthorizationRequired) {
								t.Fatalf("changed identity error = %v, want reauthorization required", err)
							}
						})
					}
				})
			}
		})
	}
}

func TestSubscriptionBatchImportSeparatesIdentityScopes(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.Codex, channel.Claude} {
		t.Run(string(channelID), func(t *testing.T) {
			f := newServiceFixture(t)
			items := []json.RawMessage{
				subscriptionIdentityJSON(t, channelID, "account", "scope-one", "first", "2099-01-01T00:00:00Z"),
				subscriptionIdentityJSON(t, channelID, "account", "scope-two", "second", "2099-01-01T00:00:00Z"),
				subscriptionIdentityJSON(t, channelID, "account", "scope-one", "renewed", "2099-01-01T00:00:00Z"),
			}
			raw, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.service.ImportCredentialBatch(t.Context(), channelID, raw, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 3 || result.Items[0].Status != "ready" || result.Items[1].Status != "ready" || result.Items[2].ErrorCode != "duplicate_account" {
				t.Fatal("batch import did not distinguish scopes and deduplicate the same identity")
			}
		})
	}
}

func TestSubscriptionBatchDeduplicatesIdentityAfterRefresh(t *testing.T) {
	t.Parallel()
	for _, ch := range []channel.ID{channel.Codex, channel.Claude} {
		t.Run(string(ch), func(t *testing.T) {
			f := newServiceFixture(t)
			driver, ok := subscriptionsDriver(f.service.subscriptions, ch)
			if !ok {
				t.Fatal("driver missing")
			}
			f.service.refreshSubscriptionCredential = func(_ context.Context, _ channel.ID, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
				var fields map[string]any
				if err := json.Unmarshal(current.Canonical(), &fields); err != nil {
					t.Fatal(err)
				}
				return driver.Parse(subscriptionIdentityJSON(t, ch, "account", "scope", fields["refresh_token"].(string), "2099-01-01T00:00:00Z"))
			}
			raw, err := json.Marshal([]json.RawMessage{
				subscriptionIdentityJSON(t, ch, "account", "", "one", "2000-01-01T00:00:00Z"),
				subscriptionIdentityJSON(t, ch, "account", "", "two", "2000-01-01T00:00:00Z"),
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.service.ImportCredentialBatch(t.Context(), ch, raw, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 2 || result.Items[0].Status != "ready" || result.Items[1].Status != "skipped" || result.Items[1].ErrorCode != "duplicate_account" {
				t.Fatal("batch did not deduplicate the identity enriched by refresh")
			}
			var count int64
			if err := f.db.Model(&models.CredentialStage{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("persisted stages = %d, want 1", count)
			}
		})
	}
}

func TestSubscriptionBatchRefreshesBeforeCoarseIdentityDeduplication(t *testing.T) {
	t.Parallel()
	for _, ch := range []channel.ID{channel.Codex, channel.Claude} {
		t.Run(string(ch), func(t *testing.T) {
			f := newServiceFixture(t)
			driver, ok := subscriptionsDriver(f.service.subscriptions, ch)
			if !ok {
				t.Fatal("driver missing")
			}
			f.service.refreshSubscriptionCredential = func(_ context.Context, _ channel.ID, current subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
				var fields map[string]any
				if err := json.Unmarshal(current.Canonical(), &fields); err != nil {
					t.Fatal(err)
				}
				return driver.Parse(subscriptionIdentityJSON(t, ch, "account", "scope", fields["refresh_token"].(string), "2099-01-01T00:00:00Z"))
			}
			raw, err := json.Marshal([]json.RawMessage{
				subscriptionIdentityJSON(t, ch, "account", "", "one", "2099-01-01T00:00:00Z"),
				subscriptionIdentityJSON(t, ch, "account", "", "two", "2000-01-01T00:00:00Z"),
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.service.ImportCredentialBatch(t.Context(), ch, raw, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 2 || result.Items[0].Status != "ready" || result.Items[1].Status != "ready" {
				t.Fatal("batch skipped a credential before refresh could complete its identity")
			}
			var count int64
			if err := f.db.Model(&models.CredentialStage{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("persisted stages = %d, want 2", count)
			}
		})
	}
}
