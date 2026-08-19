package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
)

const (
	cacheDocumentVersion = 1
	maxCacheFileBytes    = maxCatalogBodyBytes + 1024*1024
)

var cacheEnvelopeCanonicalFields = []string{
	"version",
	"etag",
	"last_modified",
	"checked_at_ms",
	"successful_fetch_at_ms",
	"raw",
}

type cacheDocument struct {
	Version                 int             `json:"version"`
	ETag                    string          `json:"etag,omitempty"`
	LastModified            string          `json:"last_modified,omitempty"`
	CheckedAtMillis         int64           `json:"checked_at_ms,omitempty"`
	SuccessfulFetchAtMillis int64           `json:"successful_fetch_at_ms"`
	Raw                     json.RawMessage `json:"raw"`
}

type replaceCatalogFile func(temporaryPath, finalPath string) error
type syncCatalogDirectoryFunc func(path string) error

// LoadCache reads and fully revalidates a bounded last-known-good document.
func LoadCache(path string) (CachedCatalog, error) {
	file, err := os.Open(path)
	if err != nil {
		return CachedCatalog{}, fmt.Errorf("open catalog cache: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return CachedCatalog{}, fmt.Errorf("stat catalog cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return CachedCatalog{}, fmt.Errorf("catalog cache must be a regular file")
	}
	if info.Size() > maxCacheFileBytes {
		return CachedCatalog{}, fmt.Errorf("catalog cache exceeds size limit")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxCacheFileBytes+1))
	if err != nil {
		return CachedCatalog{}, fmt.Errorf("read catalog cache: %w", err)
	}
	if int64(len(contents)) > maxCacheFileBytes {
		return CachedCatalog{}, fmt.Errorf("catalog cache exceeds size limit")
	}

	if _, err := decodeCanonicalObject(contents, "catalog cache envelope", cacheEnvelopeCanonicalFields); err != nil {
		return CachedCatalog{}, fmt.Errorf("validate catalog cache envelope: %w", err)
	}
	var document cacheDocument
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return CachedCatalog{}, fmt.Errorf("decode catalog cache: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CachedCatalog{}, err
	}
	if document.Version != cacheDocumentVersion {
		return CachedCatalog{}, fmt.Errorf("unsupported catalog cache version %d", document.Version)
	}
	metadata := Metadata{
		ETag:                    document.ETag,
		LastModified:            document.LastModified,
		CheckedAtMillis:         document.CheckedAtMillis,
		SuccessfulFetchAtMillis: document.SuccessfulFetchAtMillis,
	}
	if err := validateMetadata(metadata, true); err != nil {
		return CachedCatalog{}, err
	}
	if len(document.Raw) == 0 || int64(len(document.Raw)) > maxCatalogBodyBytes {
		return CachedCatalog{}, fmt.Errorf("catalog cache raw document is missing or oversized")
	}
	modelsDevSnapshot, err := Parse(bytes.NewReader(document.Raw))
	if err != nil {
		return CachedCatalog{}, fmt.Errorf("parse cached Models.dev catalog: %w", err)
	}
	snapshot, err := MergeOfficial(modelsDevSnapshot)
	if err != nil {
		return CachedCatalog{}, err
	}
	return CachedCatalog{
		Metadata: metadata,
		RawJSON:  append(json.RawMessage(nil), document.Raw...),
		Snapshot: snapshot,
	}, nil
}

// StoreCache durably replaces the last-known-good document only for a fully
// validated and internally consistent 200 response.
func StoreCache(path string, result SyncResult) error {
	return storeCache(path, result, replaceCatalogFileAtomic, syncCatalogDirectory)
}

func storeCache(
	path string,
	result SyncResult,
	replace replaceCatalogFile,
	syncDirectory syncCatalogDirectoryFunc,
) error {
	if path == "" {
		return fmt.Errorf("catalog cache path is required")
	}
	if replace == nil || syncDirectory == nil {
		return fmt.Errorf("catalog cache durability functions are required")
	}
	document, err := cacheDocumentForResult(result)
	if err != nil {
		return err
	}
	contents, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode catalog cache: %w", err)
	}
	contents = append(contents, '\n')
	if int64(len(contents)) > maxCacheFileBytes {
		return fmt.Errorf("encoded catalog cache exceeds size limit")
	}

	directory := filepath.Dir(path)
	baseName := filepath.Base(path)
	temporary, temporaryPath, err := createCatalogTemporary(directory, baseName, contents)
	if err != nil {
		return err
	}
	_ = temporary
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	backupPath, hadPrevious, err := createCatalogBackup(path, directory, baseName)
	if err != nil {
		return err
	}
	removeBackup := backupPath != ""
	defer func() {
		if removeBackup {
			_ = os.Remove(backupPath)
		}
	}()

	if err := replace(temporaryPath, path); err != nil {
		replaceErr := fmt.Errorf("replace catalog cache: %w", err)
		restoreErr := restorePreviousCatalog(path, backupPath, hadPrevious, replace, syncDirectory)
		if restoreErr == nil {
			return replaceErr
		}
		if hadPrevious {
			// A failed replace primitive does not prove the final path is unchanged.
			// Transfer ownership only after rollback fails; restore uses a separate
			// candidate so the original backup remains available here.
			removeBackup = false
		}
		restoreErr = describeRetainedCatalogBackup(backupPath, hadPrevious, restoreErr)
		return errors.Join(replaceErr, restoreErr)
	}
	removeTemporary = false
	if err := syncDirectory(path); err != nil {
		restoreErr := restorePreviousCatalog(path, backupPath, hadPrevious, replace, syncDirectory)
		if restoreErr == nil {
			return fmt.Errorf("sync catalog cache directory: %w", err)
		}
		if hadPrevious {
			removeBackup = false
		}
		restoreErr = describeRetainedCatalogBackup(backupPath, hadPrevious, restoreErr)
		return errors.Join(fmt.Errorf("sync catalog cache directory: %w", err), restoreErr)
	}

	if backupPath != "" {
		if err := os.Remove(backupPath); err != nil {
			removeBackup = false
			restoreErr := restorePreviousCatalog(path, backupPath, true, replace, syncDirectory)
			if restoreErr == nil {
				return fmt.Errorf("remove catalog cache backup: %w", err)
			}
			return errors.Join(fmt.Errorf("remove catalog cache backup: %w", err), restoreErr)
		}
		removeBackup = false
	}
	return nil
}

func describeRetainedCatalogBackup(backupPath string, hadPrevious bool, restoreErr error) error {
	if !hadPrevious || backupPath == "" || restoreErr == nil {
		return restoreErr
	}
	if _, err := os.Stat(backupPath); err == nil {
		return fmt.Errorf(
			"rollback failed; final cache path restoration is not guaranteed; byte-for-byte previous LKG retained at sibling backup %q: %w",
			backupPath,
			restoreErr,
		)
	} else if !os.IsNotExist(err) {
		return errors.Join(restoreErr, fmt.Errorf("inspect retained catalog backup %q: %w", backupPath, err))
	}
	return restoreErr
}

func cacheDocumentForResult(result SyncResult) (cacheDocument, error) {
	if result.NotModified || len(result.RawJSON) == 0 || result.Snapshot == nil {
		return cacheDocument{}, fmt.Errorf("catalog cache requires a validated 200 result")
	}
	if int64(len(result.RawJSON)) > maxCatalogBodyBytes {
		return cacheDocument{}, fmt.Errorf("catalog raw document exceeds 32 MiB limit")
	}
	if err := validateMetadata(result.Metadata, true); err != nil {
		return cacheDocument{}, err
	}
	modelsDevSnapshot, err := Parse(bytes.NewReader(result.RawJSON))
	if err != nil {
		return cacheDocument{}, fmt.Errorf("revalidate Models.dev catalog: %w", err)
	}
	reparsed, err := MergeOfficial(modelsDevSnapshot)
	if err != nil {
		return cacheDocument{}, err
	}
	if !reflect.DeepEqual(reparsed, result.Snapshot) {
		return cacheDocument{}, fmt.Errorf("catalog raw document and snapshot are inconsistent")
	}
	return cacheDocument{
		Version:                 cacheDocumentVersion,
		ETag:                    result.Metadata.ETag,
		LastModified:            result.Metadata.LastModified,
		CheckedAtMillis:         result.Metadata.CheckedAtMillis,
		SuccessfulFetchAtMillis: result.Metadata.SuccessfulFetchAtMillis,
		Raw:                     append(json.RawMessage(nil), result.RawJSON...),
	}, nil
}

func createCatalogTemporary(directory, baseName string, contents []byte) (*os.File, string, error) {
	file, err := os.CreateTemp(directory, "."+baseName+".*.tmp")
	if err != nil {
		return nil, "", fmt.Errorf("create catalog cache temporary file: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return nil, "", fmt.Errorf("secure catalog cache temporary file: %w", err)
	}
	if err := writeFull(file, contents); err != nil {
		cleanup()
		return nil, "", fmt.Errorf("write catalog cache temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return nil, "", fmt.Errorf("sync catalog cache temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", fmt.Errorf("close catalog cache temporary file: %w", err)
	}
	return file, path, nil
}

func createCatalogBackup(finalPath, directory, baseName string) (string, bool, error) {
	previous, err := os.ReadFile(finalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read previous catalog cache: %w", err)
	}
	_, backupPath, err := createCatalogTemporary(directory, baseName, previous)
	if err != nil {
		return "", false, fmt.Errorf("create catalog cache backup: %w", err)
	}
	return backupPath, true, nil
}

func restorePreviousCatalog(
	path string,
	backupPath string,
	hadPrevious bool,
	replace replaceCatalogFile,
	syncDirectory syncCatalogDirectoryFunc,
) error {
	if hadPrevious {
		if backupPath == "" {
			return fmt.Errorf("restore previous catalog cache: backup is missing")
		}
		previous, err := os.ReadFile(backupPath)
		if err != nil {
			return fmt.Errorf("read previous catalog cache backup for restore: %w", err)
		}
		_, restorePath, err := createCatalogTemporary(filepath.Dir(path), filepath.Base(path), previous)
		if err != nil {
			return fmt.Errorf("create previous catalog cache restore candidate: %w", err)
		}
		defer func() {
			_ = os.Remove(restorePath)
		}()
		if err := replace(restorePath, path); err != nil {
			return fmt.Errorf("restore previous catalog cache: %w", err)
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove uncommitted catalog cache: %w", err)
	}
	if err := syncDirectory(path); err != nil {
		return fmt.Errorf("sync restored catalog cache directory: %w", err)
	}
	return nil
}

func writeFull(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

func syncCatalogDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open catalog cache directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync catalog cache directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close catalog cache directory: %w", err)
	}
	return nil
}

func requireSiblingCatalogPaths(temporaryPath, finalPath string) error {
	if filepath.Clean(filepath.Dir(temporaryPath)) != filepath.Clean(filepath.Dir(finalPath)) {
		return fmt.Errorf("catalog cache atomic replacement requires sibling files")
	}
	return nil
}
