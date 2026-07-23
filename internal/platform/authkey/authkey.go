package authkey

import (
	"fmt"
	"strings"
	"unicode"

	"gpt-load/internal/platform/securefile"

	"github.com/sirupsen/logrus"
)

const FileName = "auth.key"

func Resolve(explicit, dataDir string) (string, error) {
	if explicit != "" {
		if strings.IndexFunc(explicit, unicode.IsSpace) >= 0 {
			return "", fmt.Errorf("AUTH_KEY must not contain whitespace")
		}
		return explicit, nil
	}

	result, err := securefile.LoadOrCreateHex(dataDir, FileName)
	if err != nil {
		return "", fmt.Errorf("resolve AUTH_KEY: %w", err)
	}
	if result.Created {
		logrus.WithField("path", result.Path).
			Warn("Generated AUTH_KEY file; read it from this path and store it securely")
	}
	return result.Value, nil
}
