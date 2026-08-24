package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestListGroupOptionsReturnsAllGroupsByIDWithExternalModels(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	createGroupOptionGroup(t, fixture, 20, "later enabled", true,
		channel.OpenAICompatible, `{"base_url":"https://later-enabled.example/v1"}`,
		`[{"id":" upstream-first ","alias":" public-first "},{"id":" upstream-second ","alias":"   "}]`,
	)
	createGroupOptionGroup(t, fixture, 10, "earlier disabled", false,
		channel.Anthropic, `{}`,
		`[{"id":" upstream-third ","alias":""},{"id":" upstream-fourth ","alias":" public-fourth "}]`,
	)
	if err := fixture.db.Create(&models.Credential{
		GroupID: 10, Data: "ciphertext-secret", Fingerprint: "hash-secret",
		Status: models.CredentialStatusActive,
	}).Error; err != nil {
		t.Fatalf("create unrelated upstream key: %v", err)
	}

	options, err := fixture.service.ListGroupOptions(t.Context())
	if err != nil {
		t.Fatalf("ListGroupOptions() error = %v", err)
	}
	if len(options) != 2 || options[0].ID != 10 || options[0].Name != "earlier disabled" ||
		options[0].ChannelID != channel.Anthropic || string(options[0].Params) != `{}` ||
		options[0].Enabled || !reflect.DeepEqual(options[0].Models, []string{"upstream-third", "public-fourth"}) ||
		options[1].ID != 20 || options[1].Name != "later enabled" ||
		options[1].ChannelID != channel.OpenAICompatible ||
		string(options[1].Params) != `{"base_url":"https://later-enabled.example/v1"}` ||
		!options[1].Enabled || !reflect.DeepEqual(options[1].Models, []string{"public-first", "upstream-second"}) {
		t.Fatalf("ListGroupOptions() = %#v, want terminal channel options", options)
	}

	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatalf("json.Marshal(options) error = %v", err)
	}
	for _, forbiddenField := range []string{
		"upstream_url", "protocols", "key_value", "key_hash", "keyvalue", "keyhash",
	} {
		if containsJSONToken(encoded, forbiddenField) {
			t.Fatalf("options JSON exposes field %q: %s", forbiddenField, encoded)
		}
	}
	for _, forbiddenValue := range []string{"ciphertext-secret", "hash-secret", "plaintext-secret", "secret"} {
		if strings.Contains(string(encoded), forbiddenValue) {
			t.Fatalf("options JSON exposes %q: %s", forbiddenValue, encoded)
		}
	}
}

func TestListGroupOptionsFailsClosedForInvalidDataDatabaseAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("invalid models JSON", func(t *testing.T) {
		fixture := newServiceFixture(t)
		createGroupOptionGroup(t, fixture, 1, "invalid", true, channel.OpenAI, `{}`, `{"not":"an array"}`)

		options, err := fixture.service.ListGroupOptions(t.Context())
		if options != nil || !errors.Is(err, app_errors.ErrInternalServer) {
			t.Fatalf("ListGroupOptions() = %#v, %v; want nil, ErrInternalServer", options, err)
		}
	})

	for name, test := range map[string]struct {
		channelID channel.ID
		params    string
	}{
		"invalid params shape": {channelID: channel.OpenAICompatible, params: `[]`},
		"invalid params":       {channelID: channel.OpenAICompatible, params: `{"base_url":"relative"}`},
		"unknown channel":      {channelID: channel.ID("unknown"), params: `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			createGroupOptionGroup(t, fixture, 1, "invalid", true, test.channelID, test.params, `[]`)

			options, err := fixture.service.ListGroupOptions(t.Context())
			if options != nil || !errors.Is(err, app_errors.ErrInternalServer) {
				t.Fatalf("ListGroupOptions() = %#v, %v; want nil, ErrInternalServer", options, err)
			}
		})
	}

	t.Run("database error", func(t *testing.T) {
		fixture := newServiceFixture(t)
		sqlDB, err := fixture.db.DB()
		if err != nil {
			t.Fatalf("fixture DB(): %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close fixture DB: %v", err)
		}

		options, gotErr := fixture.service.ListGroupOptions(t.Context())
		if options != nil || !errors.Is(gotErr, app_errors.ErrDatabase) {
			t.Fatalf("ListGroupOptions() = %#v, %v; want nil, ErrDatabase", options, gotErr)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		fixture := newServiceFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		options, err := fixture.service.ListGroupOptions(ctx)
		if options != nil || err != context.Canceled {
			t.Fatalf("ListGroupOptions() = %#v, %v; want nil, context.Canceled", options, err)
		}
	})
}

func createGroupOptionGroup(
	t *testing.T,
	fixture serviceFixture,
	id uint,
	name string,
	enabled bool,
	channelID channel.ID,
	rawParams string,
	rawModels string,
) {
	t.Helper()
	group := validControlGroup(name)
	group.ID = id
	group.ChannelID = string(channelID)
	group.Params = models.JSON(rawParams)
	group.Models = models.JSON(rawModels)
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	if !enabled {
		if err := fixture.db.Model(group).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable group %q: %v", name, err)
		}
	}
}
