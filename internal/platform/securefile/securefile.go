package securefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	encodedMaterialBytes  = 64
	temporaryFileAttempts = 10
)

type Result struct {
	Value   string
	Path    string
	Created bool
}

func LoadOrCreateHex(dataDir, fileName string) (Result, error) {
	return loadOrCreateHex(dataDir, fileName, syncParentDirectory)
}

func loadOrCreateHex(
	dataDir string,
	fileName string,
	syncDirectory func(string) error,
) (Result, error) {
	if dataDir == "" {
		return Result{}, fmt.Errorf("data directory is required")
	}
	if fileName == "" || fileName == "." || fileName == ".." ||
		strings.ContainsAny(fileName, `/\`) ||
		filepath.Base(fileName) != fileName {
		return Result{}, fmt.Errorf("secure filename must be a basename")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create data directory: %w", err)
	}

	path := filepath.Join(dataDir, fileName)
	if material, err := loadDurableHex(path, syncDirectory); err == nil {
		return Result{Value: material, Path: path}, nil
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}

	randomBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, randomBytes); err != nil {
		return Result{}, fmt.Errorf("generate secure material: %w", err)
	}
	material := hex.EncodeToString(randomBytes)
	if err := persistGeneratedHex(
		path,
		fileName,
		material,
		publishSecureFile,
		syncDirectory,
	); err != nil {
		if errors.Is(err, os.ErrExist) {
			value, loadErr := loadDurableHex(path, syncDirectory)
			if loadErr != nil {
				return Result{}, loadErr
			}
			return Result{Value: value, Path: path}, nil
		}
		return Result{}, err
	}
	return Result{Value: material, Path: path, Created: true}, nil
}

func persistGeneratedHex(
	path string,
	fileName string,
	material string,
	publish func(temporaryPath, finalPath string) error,
	syncDirectory func(string) error,
) error {
	file, temporaryPath, err := createSecureTemporaryFile(filepath.Dir(path), fileName)
	if err != nil {
		return fmt.Errorf("create temporary secure file: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	contents := material + "\n"
	if written, err := file.WriteString(contents); err != nil || written != len(contents) {
		cleanupFile(file, temporaryPath)
		if err == nil {
			err = io.ErrShortWrite
		}
		return fmt.Errorf("write temporary secure file: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanupFile(file, temporaryPath)
		return fmt.Errorf("sync temporary secure file: %w", err)
	}
	if err := secureOpenedFile(file); err != nil {
		cleanupFile(file, temporaryPath)
		return fmt.Errorf("secure temporary secure file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary secure file: %w", err)
	}
	if err := publish(temporaryPath, path); err != nil {
		return fmt.Errorf("publish secure file: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove temporary secure file: %w", err)
	}
	removeTemporary = false
	if err := syncDirectory(path); err != nil {
		return fmt.Errorf("sync secure file directory: %w", err)
	}
	return nil
}

func createSecureTemporaryFile(dataDir, fileName string) (*os.File, string, error) {
	for range temporaryFileAttempts {
		suffix := make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, suffix); err != nil {
			return nil, "", fmt.Errorf("generate temporary secure file name: %w", err)
		}
		path := filepath.Join(dataDir, "."+fileName+"."+hex.EncodeToString(suffix)+".tmp")
		file, err := createSecureFile(path)
		if err == nil {
			return file, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate a unique temporary secure file")
}

func loadDurableHex(path string, syncDirectory func(string) error) (string, error) {
	material, err := readHexFile(path)
	if err != nil {
		return "", err
	}
	if err := syncDirectory(path); err != nil {
		return "", fmt.Errorf("sync secure file directory: %w", err)
	}
	return material, nil
}

func readHexFile(path string) (string, error) {
	file, err := openExistingSecureFile(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	return readOpenedHex(file, path)
}

func readOpenedHex(file *os.File, path string) (string, error) {
	if err := requireRegularFile(file, path); err != nil {
		return "", err
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	material := strings.TrimSpace(string(contents))
	if len(material) != encodedMaterialBytes {
		return "", fmt.Errorf("secure file %s must contain exactly 64 hex characters", path)
	}
	decoded, err := hex.DecodeString(material)
	if err != nil {
		return "", fmt.Errorf("secure file %s contains invalid hex: %w", path, err)
	}
	if len(decoded) != 32 {
		return "", fmt.Errorf("secure file %s must decode to 32 bytes", path)
	}
	if err := secureOpenedFile(file); err != nil {
		return "", fmt.Errorf("secure file %s: %w", path, err)
	}
	return material, nil
}

func requireRegularFile(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secure file %s must be a regular file", path)
	}
	return nil
}

func cleanupFile(file *os.File, path string) {
	_ = file.Close()
	_ = os.Remove(path)
}
