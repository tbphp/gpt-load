package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestListGroupOptionsReturnsAllGroupsByIDWithExternalModels(t *testing.T) {
	fixture := newServiceFixture(t)
	createGroupOptionGroup(t, fixture, 20, "later enabled", true,
		`[{"id":" upstream-first ","alias":" public-first "},{"id":" upstream-second ","alias":"   "}]`,
	)
	createGroupOptionGroup(t, fixture, 10, "earlier disabled", false,
		`[{"id":" upstream-third ","alias":""},{"id":" upstream-fourth ","alias":" public-fourth "}]`,
	)
	if err := fixture.db.Create(&models.UpstreamKey{
		GroupID: 10, KeyValue: "ciphertext-secret", KeyHash: "hash-secret",
		Status: models.UpstreamKeyStatusActive,
	}).Error; err != nil {
		t.Fatalf("create unrelated upstream key: %v", err)
	}

	options, err := fixture.service.ListGroupOptions(t.Context())
	if err != nil {
		t.Fatalf("ListGroupOptions() error = %v", err)
	}
	want := []GroupOption{
		{ID: 10, Name: "earlier disabled", Models: []string{"upstream-third", "public-fourth"}},
		{ID: 20, Name: "later enabled", Models: []string{"public-first", "upstream-second"}},
	}
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("ListGroupOptions() = %#v, want %#v", options, want)
	}

	encoded, err := json.Marshal(options)
	if err != nil {
		t.Fatalf("json.Marshal(options) error = %v", err)
	}
	for _, forbiddenField := range []string{
		"upstream_url", "enabled", "protocols", "key_value", "key_hash",
	} {
		if containsJSONToken(encoded, forbiddenField) {
			t.Fatalf("options JSON exposes field %q: %s", forbiddenField, encoded)
		}
	}
	for _, forbiddenValue := range []string{"ciphertext-secret", "hash-secret", "secret"} {
		if strings.Contains(string(encoded), forbiddenValue) {
			t.Fatalf("options JSON exposes %q: %s", forbiddenValue, encoded)
		}
	}
}

func TestListGroupOptionsFailsClosedForInvalidDataDatabaseAndCancellation(t *testing.T) {
	t.Run("invalid models JSON", func(t *testing.T) {
		fixture := newServiceFixture(t)
		createGroupOptionGroup(t, fixture, 1, "invalid", true, `{"not":"an array"}`)

		options, err := fixture.service.ListGroupOptions(t.Context())
		if options != nil || !errors.Is(err, app_errors.ErrInternalServer) {
			t.Fatalf("ListGroupOptions() = %#v, %v; want nil, ErrInternalServer", options, err)
		}
	})

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
	rawModels string,
) {
	t.Helper()
	group := validControlGroup(name)
	group.ID = id
	group.Models = models.JSON(rawModels)
	group.UpstreamURL = "https://" + strings.ReplaceAll(name, " ", "-") + ".example/v1"
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}
	if !enabled {
		if err := fixture.db.Model(group).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable group %q: %v", name, err)
		}
	}
}
