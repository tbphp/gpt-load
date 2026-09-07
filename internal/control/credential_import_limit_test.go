package control

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	app_errors "gpt-load/internal/platform/errors"
)

func TestAPIKeyImportAccepts5000AndReplaysWithoutDuplicates(t *testing.T) {
	fixture, _ := newFileServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-synthetic-existing")
	if err := fixture.service.DeleteGroupCredential(t.Context(), groupID, fixture.registry.Snapshot()[0].ID); err != nil {
		t.Fatalf("remove seed credential: %v", err)
	}
	credentials := syntheticImportCredentials(5000)
	request := CredentialImportRequest{Credentials: "\n \r\n" + credentials + "\n\n"}
	const idempotencyKey = "00000000-0000-4000-8000-000000005000"
	beforeSnapshot := fixture.manager.Current()

	started := time.Now()
	first, err := fixture.service.ImportGroupCredentialsIdempotent(t.Context(), idempotencyKey, groupID, request)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("import 5000 API keys: %v", err)
	}
	t.Logf("file-backed SQLite synchronous import of 5000 synthetic API keys: %s", elapsed)
	if first.GroupID != groupID || first.CredentialsAdded != 5000 || first.CredentialsDuplicated != 0 {
		t.Fatalf("import result = %#v, want 5000 added and 0 duplicates", first)
	}
	assertImportedCredentialState(t, fixture, groupID, 5000)
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("credential import republished the configuration snapshot")
	}
	beforeReplay := countCreateImportRows(t, fixture)
	replayed, err := fixture.service.ImportGroupCredentialsIdempotent(t.Context(), idempotencyKey, groupID, request)
	if err != nil || replayed != first {
		t.Fatalf("replay result/error = %#v/%v, want original result %#v", replayed, err, first)
	}
	if afterReplay := countCreateImportRows(t, fixture); afterReplay != beforeReplay {
		t.Fatalf("replay changed resource counts: before=%#v after=%#v", beforeReplay, afterReplay)
	}
	duplicate, err := fixture.service.ImportGroupCredentialsIdempotent(
		t.Context(), "00000000-0000-4000-8000-000000005002", groupID, request,
	)
	if err != nil || duplicate.CredentialsAdded != 0 || duplicate.CredentialsDuplicated != 5000 {
		t.Fatalf("duplicate import result/error = %#v/%v, want 0 added and 5000 duplicates", duplicate, err)
	}
	assertImportedCredentialState(t, fixture, groupID, 5000)

	started = time.Now()
	download, err := fixture.service.DownloadAllGroupCredentials(t.Context(), groupID)
	elapsed = time.Since(started)
	if err != nil {
		t.Fatalf("download 5000 API keys: %v", err)
	}
	t.Logf("file-backed SQLite synchronous download of 5000 synthetic API keys: %s", elapsed)
	if download.CredentialCount != 5000 || len(download.Files) != 1 {
		t.Fatalf("download credential/file count = %d/%d, want 5000/1", download.CredentialCount, len(download.Files))
	}
	content := download.Files[0].Content
	if content == nil || strings.Count(*content, "\n") != 5000 || *content != credentials+"\n" {
		t.Fatal("download content must contain the same 5000 API keys in order, each followed by a newline")
	}
}

func TestAPIKeyImportRejects5001NonEmptyEntriesWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, idempotent := range []bool{false, true} {
		for _, repeated := range []bool{false, true} {
			t.Run(fmt.Sprintf("idempotent=%t/repeated=%t", idempotent, repeated), func(t *testing.T) {
				fixture := newServiceFixture(t)
				groupID := createGroupForCredentialImport(t, fixture, "sk-synthetic-existing")
				raw := syntheticImportCredentials(5001)
				if repeated {
					raw = strings.Repeat("sk-synthetic-repeated\n", 5001)
				}
				request := CredentialImportRequest{Credentials: raw}
				beforeRows := countCreateImportRows(t, fixture)
				beforeSnapshot := fixture.manager.Current()
				var err error
				if idempotent {
					_, err = fixture.service.ImportGroupCredentialsIdempotent(
						t.Context(), "00000000-0000-4000-8000-000000005001", groupID, request,
					)
				} else {
					_, err = fixture.service.ImportGroupCredentials(t.Context(), groupID, request)
				}
				if !errors.Is(err, app_errors.ErrValidation) {
					t.Fatalf("import 5001 non-empty entries error = %v, want validation error", err)
				}
				if afterRows := countCreateImportRows(t, fixture); afterRows != beforeRows {
					t.Fatalf("rejected import changed resource counts: before=%#v after=%#v", beforeRows, afterRows)
				}
				if fixture.manager.Current() != beforeSnapshot || len(fixture.registry.Snapshot()) != 1 {
					t.Fatal("rejected import changed runtime state")
				}
			})
		}
	}
}

func TestSubscriptionCredentialStageLimitStays1000(t *testing.T) {
	t.Parallel()
	stages := make([]string, 1001)
	for index := range stages {
		stages[index] = fmt.Sprintf("synthetic-stage-%04d", index)
	}
	if normalized, err := normalizeCredentialStageIDs(stages[:1000]); err != nil || len(normalized) != 1000 {
		t.Fatalf("normalize 1000 subscription stages count/error = %d/%v", len(normalized), err)
	}
	if _, err := normalizeCredentialStageIDs(stages); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("normalize 1001 subscription stages error = %v, want validation error", err)
	}
}

func syntheticImportCredentials(count int) string {
	lines := make([]string, count)
	for index := range lines {
		lines[index] = fmt.Sprintf("sk-synthetic-import-%048d", index)
	}
	return strings.Join(lines, "\n")
}
