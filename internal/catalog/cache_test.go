package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCacheRoundTripStoresRawJSONValueAndReparsesSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.dev.catalog.json")
	result := validSyncResult(t)
	if err := StoreCache(path, result); err != nil {
		t.Fatalf("StoreCache() error = %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(cache) error = %v", err)
	}
	if bytes.Contains(contents, []byte(`"raw":"`)) || !bytes.Contains(contents, []byte(`"raw":{`)) {
		t.Fatalf("cache raw payload is not an embedded JSON value: %s", contents)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("cache document JSON error = %v", err)
	}
	if string(document["version"]) != "1" {
		t.Fatalf("cache version = %s, want 1", document["version"])
	}

	loaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if loaded.Metadata != result.Metadata || string(loaded.RawJSON) != string(result.RawJSON) || !reflect.DeepEqual(loaded.Snapshot, result.Snapshot) {
		t.Fatalf("loaded cache = %#v, want result %#v", loaded, result)
	}
	loadedProvider := loaded.Snapshot.Providers["openai"]
	loadedModel := loadedProvider.Models["gpt-x"]
	*loadedModel.Metadata.Capabilities.Attachment = false
	loadedModel.Metadata.Modalities.Input[0] = "audio"
	*loadedModel.Metadata.Limits.Context = 99
	loadedProvider.Models["gpt-x"] = loadedModel
	loaded.Snapshot.Providers["openai"] = loadedProvider
	sourceMetadata := result.Snapshot.Providers["openai"].Models["gpt-x"].Metadata
	if sourceMetadata.Capabilities.Attachment == nil || !*sourceMetadata.Capabilities.Attachment ||
		sourceMetadata.Modalities.Input[0] != "text" ||
		sourceMetadata.Limits.Context == nil || *sourceMetadata.Limits.Context != 1_000_000 {
		t.Fatalf("loaded cache shares snapshot metadata with source: %#v", sourceMetadata)
	}
	reloaded, err := LoadCache(path)
	if err != nil {
		t.Fatalf("LoadCache() after caller mutation error = %v", err)
	}
	reloadedMetadata := reloaded.Snapshot.Providers["openai"].Models["gpt-x"].Metadata
	if reloadedMetadata.Capabilities.Attachment == nil || !*reloadedMetadata.Capabilities.Attachment ||
		reloadedMetadata.Modalities.Input[0] != "text" ||
		reloadedMetadata.Limits.Context == nil || *reloadedMetadata.Limits.Context != 1_000_000 {
		t.Fatalf("caller mutation changed cached metadata: %#v", reloadedMetadata)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(cache) error = %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("cache mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestLoadCacheRejectsUnsupportedCorruptAndOversizeDocuments(t *testing.T) {
	validRaw := `{"openai":{"id":"openai","name":"OpenAI","models":{}}}`
	tests := []struct {
		name     string
		contents string
	}{
		{name: "duplicate version", contents: `{"version":2,"version":1,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "duplicate raw", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":{},"raw":` + validRaw + `}`},
		{name: "duplicate etag", contents: `{"version":1,"etag":"old","etag":"new","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "duplicate last modified", contents: `{"version":1,"last_modified":"old","last_modified":"new","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "duplicate checked timestamp", contents: `{"version":1,"checked_at_ms":2,"checked_at_ms":3,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "duplicate successful timestamp", contents: `{"version":1,"successful_fetch_at_ms":0,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "version case alias", contents: `{"version":2,"Version":1,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "raw case alias", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":{},"Raw":` + validRaw + `}`},
		{name: "etag case alias", contents: `{"version":1,"etag":"old","ETag":"new","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "last modified case alias", contents: `{"version":1,"last_modified":"old","Last_Modified":"new","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "checked timestamp case alias", contents: `{"version":1,"checked_at_ms":2,"Checked_At_MS":3,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "successful timestamp case alias", contents: `{"version":1,"successful_fetch_at_ms":0,"Successful_Fetch_At_MS":1,"raw":` + validRaw + `}`},
		{name: "unsupported version", contents: `{"version":2,"successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "zero successful fetch time", contents: `{"version":1,"successful_fetch_at_ms":0,"raw":` + validRaw + `}`},
		{name: "negative successful fetch time", contents: `{"version":1,"successful_fetch_at_ms":-1,"raw":` + validRaw + `}`},
		{name: "invalid etag control", contents: `{"version":1,"etag":"bad\nvalue","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "invalid last modified control", contents: `{"version":1,"last_modified":"bad\nvalue","successful_fetch_at_ms":1,"raw":` + validRaw + `}`},
		{name: "missing raw", contents: `{"version":1,"successful_fetch_at_ms":1}`},
		{name: "raw string instead object", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":"` + strings.ReplaceAll(validRaw, `"`, `\"`) + `"}`},
		{name: "invalid raw snapshot", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":{"openai":{"id":"wrong","name":"OpenAI","models":{}}}}`},
		{name: "duplicate raw provider", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":{"openai":{"id":"openai","name":"OpenAI","models":{}},"openai":{"id":"openai","name":"Other","models":{}}}}`},
		{name: "trailing cache JSON", contents: `{"version":1,"successful_fetch_at_ms":1,"raw":` + validRaw + `}{}`},
		{name: "malformed cache JSON", contents: `{"version":1`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "models.dev.catalog.json")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			if _, err := LoadCache(path); err == nil {
				t.Fatalf("LoadCache() accepted invalid document: %s", test.contents)
			}
		})
	}

	t.Run("oversize file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "models.dev.catalog.json")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("OpenFile() error = %v", err)
		}
		if err := file.Truncate(maxCacheFileBytes + 1); err != nil {
			_ = file.Close()
			t.Fatalf("Truncate() error = %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := LoadCache(path); err == nil {
			t.Fatal("LoadCache() accepted oversize file")
		}
	})
}

func TestStoreCacheAcceptsOnlyInternallyConsistentValidated200Result(t *testing.T) {
	valid := validSyncResult(t)
	tests := []struct {
		name   string
		mutate func(*SyncResult)
	}{
		{name: "not modified", mutate: func(result *SyncResult) { result.NotModified = true }},
		{name: "missing raw", mutate: func(result *SyncResult) { result.RawJSON = nil }},
		{name: "missing snapshot", mutate: func(result *SyncResult) { result.Snapshot = nil }},
		{name: "invalid raw", mutate: func(result *SyncResult) { result.RawJSON = json.RawMessage(`{}` + `{}`) }},
		{name: "snapshot mismatch", mutate: func(result *SyncResult) {
			result.Snapshot = &Snapshot{Providers: map[string]Provider{}}
		}},
		{name: "missing successful fetch time", mutate: func(result *SyncResult) {
			result.Metadata.SuccessfulFetchAtMillis = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			test.mutate(&result)
			path := filepath.Join(t.TempDir(), "models.dev.catalog.json")
			if err := StoreCache(path, result); err == nil {
				t.Fatal("StoreCache() accepted invalid result")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid result created cache file: %v", err)
			}
		})
	}
}

func TestCacheFailedReplacementKeepsPreviousFileAndRemovesTemporary(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	previous := []byte("previous last known good bytes\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error = %v", err)
	}
	replaceErr := errors.New("injected atomic replacement failure")
	replaceCalls := 0
	syncCalls := 0
	err := storeCache(
		path,
		validSyncResult(t),
		func(temporaryPath, finalPath string) error {
			replaceCalls++
			if finalPath != path || filepath.Dir(temporaryPath) != directory {
				t.Fatalf("replace paths = %q -> %q", temporaryPath, finalPath)
			}
			if replaceCalls == 1 {
				return replaceErr
			}
			return os.Rename(temporaryPath, finalPath)
		},
		func(string) error {
			syncCalls++
			return nil
		},
	)
	if !errors.Is(err, replaceErr) || replaceCalls != 2 || syncCalls != 1 {
		t.Fatalf("storeCache() error/replace/sync calls = %v/%d/%d, want injected error/2/1", err, replaceCalls, syncCalls)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(previous) error = %v", err)
	}
	if !bytes.Equal(got, previous) {
		t.Fatalf("previous LKG changed: got %q, want %q", got, previous)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".models.dev.catalog.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(temp files) error = %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files remain after failure: %#v", temporaryFiles)
	}
}

func TestCacheReplaceErrorAfterChangingFinalRestoresPreviousFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	previous := []byte("previous LKG before uncertain replacement\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error = %v", err)
	}
	replaceErr := errors.New("replace reported failure after changing final")
	replaceCalls := 0
	syncCalls := 0
	err := storeCache(
		path,
		validSyncResult(t),
		func(temporaryPath, finalPath string) error {
			replaceCalls++
			if replaceCalls == 1 {
				if err := os.Rename(temporaryPath, finalPath); err != nil {
					t.Fatalf("simulate uncertain replacement: %v", err)
				}
				return replaceErr
			}
			return os.Rename(temporaryPath, finalPath)
		},
		func(string) error {
			syncCalls++
			return nil
		},
	)
	if !errors.Is(err, replaceErr) || replaceCalls != 2 || syncCalls != 1 {
		t.Fatalf("storeCache() error/replace/sync calls = %v/%d/%d, want original error/2/1", err, replaceCalls, syncCalls)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(restored final) error = %v", err)
	}
	if !bytes.Equal(got, previous) {
		t.Fatalf("restored final = %q, want previous bytes %q", got, previous)
	}
	assertNoCatalogTemporaryFiles(t, directory)
}

func TestCacheReplaceErrorAndRollbackFailureRetainsPreviousBackupPath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	previous := []byte("previous LKG retained after uncertain replacement\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error = %v", err)
	}
	replaceErr := errors.New("replace reported failure after changing final")
	rollbackErr := errors.New("rollback primitive failed")
	replaceCalls := 0
	storeErr := storeCache(
		path,
		validSyncResult(t),
		func(temporaryPath, finalPath string) error {
			replaceCalls++
			if replaceCalls == 1 {
				if err := os.Rename(temporaryPath, finalPath); err != nil {
					t.Fatalf("simulate uncertain replacement: %v", err)
				}
				return replaceErr
			}
			if err := os.Rename(temporaryPath, finalPath); err != nil {
				t.Fatalf("simulate uncertain rollback replacement: %v", err)
			}
			return rollbackErr
		},
		func(string) error {
			t.Fatal("directory sync called after failed rollback primitive")
			return nil
		},
	)
	if !errors.Is(storeErr, replaceErr) || !errors.Is(storeErr, rollbackErr) || replaceCalls != 2 {
		t.Fatalf("storeCache() error/calls = %v/%d, want both errors and two replacements", storeErr, replaceCalls)
	}
	backupFiles, err := filepath.Glob(filepath.Join(directory, ".models.dev.catalog.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(backup files) error = %v", err)
	}
	if len(backupFiles) != 1 || !strings.Contains(storeErr.Error(), backupFiles[0]) {
		t.Fatalf("retained backups/error = %#v/%q, want one explicit recovery path", backupFiles, storeErr)
	}
	backup, err := os.ReadFile(backupFiles[0])
	if err != nil {
		t.Fatalf("ReadFile(retained backup) error = %v", err)
	}
	if !bytes.Equal(backup, previous) {
		t.Fatalf("retained backup = %q, want previous bytes %q", backup, previous)
	}
}

func TestCacheReplaceErrorWithoutPreviousFileRemovesPossibleFinal(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	replaceErr := errors.New("first publication reported failure after changing final")
	syncCalls := 0
	err := storeCache(
		path,
		validSyncResult(t),
		func(temporaryPath, finalPath string) error {
			if err := os.Rename(temporaryPath, finalPath); err != nil {
				t.Fatalf("simulate uncertain first publication: %v", err)
			}
			return replaceErr
		},
		func(string) error {
			syncCalls++
			return nil
		},
	)
	if !errors.Is(err, replaceErr) || syncCalls != 1 {
		t.Fatalf("storeCache() error/sync calls = %v/%d, want original error/1", err, syncCalls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("uncertain first publication final still exists: %v", err)
	}
	assertNoCatalogTemporaryFiles(t, directory)
}

func TestCacheDirectorySyncFailureAfterReplacementRestoresPreviousFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	previous := []byte("previous durable last known good bytes\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error = %v", err)
	}
	syncErr := errors.New("injected parent directory sync failure")
	syncCalls := 0
	err := storeCache(
		path,
		validSyncResult(t),
		os.Rename,
		func(string) error {
			syncCalls++
			if syncCalls == 1 {
				return syncErr
			}
			return nil
		},
	)
	if !errors.Is(err, syncErr) {
		t.Fatalf("storeCache() error = %v, want injected directory sync error", err)
	}
	if syncCalls < 2 {
		t.Fatalf("directory sync calls = %d, want replacement failure plus restored-LKG sync", syncCalls)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(restored LKG) error = %v", err)
	}
	if !bytes.Equal(got, previous) {
		t.Fatalf("directory sync failure left changed LKG: got %q, want %q", got, previous)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".models.dev.catalog.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(temp files) error = %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary/backup files remain after restore: %#v", temporaryFiles)
	}
}

func TestCacheRollbackReplaceFailureKeepsRecoverablePreviousBackup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "models.dev.catalog.json")
	previous := []byte("only previous durable last known good bytes\n")
	if err := os.WriteFile(path, previous, 0o600); err != nil {
		t.Fatalf("WriteFile(previous) error = %v", err)
	}
	syncErr := errors.New("injected parent directory sync failure")
	rollbackErr := errors.New("injected rollback replace failure")
	replaceCalls := 0
	storeErr := storeCache(
		path,
		validSyncResult(t),
		func(temporaryPath, finalPath string) error {
			replaceCalls++
			if replaceCalls == 1 {
				return os.Rename(temporaryPath, finalPath)
			}
			return rollbackErr
		},
		func(string) error { return syncErr },
	)
	if !errors.Is(storeErr, syncErr) || !errors.Is(storeErr, rollbackErr) || replaceCalls != 2 {
		t.Fatalf("storeCache() error/calls = %v/%d, want both injected errors and two replacements", storeErr, replaceCalls)
	}
	backupFiles, err := filepath.Glob(filepath.Join(directory, ".models.dev.catalog.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(backup files) error = %v", err)
	}
	if len(backupFiles) != 1 {
		t.Fatalf("recoverable backup files = %#v, want exactly one retained backup", backupFiles)
	}
	if !strings.Contains(storeErr.Error(), backupFiles[0]) {
		t.Fatalf("storeCache() error = %q, want explicit recoverable backup path %q", storeErr, backupFiles[0])
	}
	backup, err := os.ReadFile(backupFiles[0])
	if err != nil {
		t.Fatalf("ReadFile(retained backup) error = %v", err)
	}
	if !bytes.Equal(backup, previous) {
		t.Fatalf("retained backup = %q, want previous LKG bytes %q", backup, previous)
	}
	finalContents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(final after failed rollback) error = %v", err)
	}
	if bytes.Equal(finalContents, previous) {
		t.Fatal("failed rollback unexpectedly reports the final path as restored; recovery guarantee belongs to the sibling backup")
	}
}

func assertNoCatalogTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	temporaryFiles, err := filepath.Glob(filepath.Join(directory, ".models.dev.catalog.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(temp files) error = %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files remain: %#v", temporaryFiles)
	}
}

func validSyncResult(t *testing.T) SyncResult {
	t.Helper()
	raw := json.RawMessage(`{"openai":{"id":"openai","name":"OpenAI","models":{"gpt-x":{"id":"gpt-x","name":"GPT X","description":"General model","attachment":true,"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":1000000,"output":100000},"cost":{"input":0.000000001}}}}}`)
	return SyncResult{
		Metadata: Metadata{
			ETag:                    `"catalog-v1"`,
			LastModified:            "Mon, 03 Aug 2026 01:00:00 GMT",
			CheckedAtMillis:         1_754_180_400_123,
			SuccessfulFetchAtMillis: 1_754_180_400_123,
		},
		RawJSON:  raw,
		Snapshot: mustParse(t, string(raw)),
	}
}
