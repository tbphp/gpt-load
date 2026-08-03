package control

import (
	"crypto/sha256"
	"encoding/hex"
	"net/textproto"
	"sort"
	"strings"

	"gpt-load/internal/platform/canonicaljson"
)

const settingsWireETagDomain = "gpt-load/settings-wire-v1"

type SettingsDTO struct {
	Values    SettingsValuesResponse `json:"values"`
	Overrides []string               `json:"overrides"`
	ReadOnly  []string               `json:"read_only,omitempty"`
}

type SettingsConflictData struct {
	Settings     SettingsDTO `json:"settings"`
	SettingsETag string      `json:"settings_etag"`
}

type settingsWireRepresentation struct {
	Settings   SettingsDTO
	Body       []byte
	ETag       string
	HeaderETag string
}

func parseSettingsHeaderETag(value string) (string, bool) {
	if len(value) != 73 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	token := value[1 : len(value)-1]
	if !strings.HasPrefix(token, "sha256-") {
		return "", false
	}
	for _, character := range token[len("sha256-"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return "", false
			}
		}
	}
	return token, true
}

type settingsSuccessEnvelope struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    SettingsDTO `json:"data"`
}

func newSettingsWireRepresentation(
	message string,
	settings SettingsDTO,
) (settingsWireRepresentation, error) {
	settings = canonicalizeSettingsDTO(settings)
	body, err := canonicaljson.Marshal(settingsSuccessEnvelope{
		Code:    0,
		Message: message,
		Data:    settings,
	})
	if err != nil {
		return settingsWireRepresentation{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(settingsWireETagDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(body)
	tag := "sha256-" + hex.EncodeToString(hash.Sum(nil))
	return settingsWireRepresentation{
		Settings:   settings,
		Body:       body,
		ETag:       tag,
		HeaderETag: `"` + tag + `"`,
	}, nil
}

func canonicalizeSettingsDTO(settings SettingsDTO) SettingsDTO {
	setNames := make([]string, 0, len(settings.Values.HeaderRules.Set))
	for name := range settings.Values.HeaderRules.Set {
		setNames = append(setNames, name)
	}
	sort.Slice(setNames, func(left, right int) bool {
		leftFolded := strings.ToLower(setNames[left])
		rightFolded := strings.ToLower(setNames[right])
		if leftFolded == rightFolded {
			return setNames[left] < setNames[right]
		}
		return leftFolded < rightFolded
	})
	set := make(map[string]string, len(setNames))
	for _, name := range setNames {
		set[textproto.CanonicalMIMEHeaderKey(name)] = settings.Values.HeaderRules.Set[name]
	}

	removeByIdentity := make(map[string]string, len(settings.Values.HeaderRules.Remove))
	for _, name := range settings.Values.HeaderRules.Remove {
		canonicalName := textproto.CanonicalMIMEHeaderKey(name)
		removeByIdentity[strings.ToLower(canonicalName)] = canonicalName
	}
	remove := make([]string, 0, len(removeByIdentity))
	for _, name := range removeByIdentity {
		remove = append(remove, name)
	}
	sort.Strings(remove)

	overrideSet := make(map[string]struct{}, len(settings.Overrides))
	for _, override := range settings.Overrides {
		overrideSet[override] = struct{}{}
	}
	overrides := make([]string, 0, len(overrideSet))
	for override := range overrideSet {
		overrides = append(overrides, override)
	}
	sort.Strings(overrides)
	readOnlySet := make(map[string]struct{}, len(settings.ReadOnly))
	for _, key := range settings.ReadOnly {
		readOnlySet[key] = struct{}{}
	}
	readOnly := make([]string, 0, len(readOnlySet))
	for key := range readOnlySet {
		readOnly = append(readOnly, key)
	}
	sort.Strings(readOnly)

	settings.Values.HeaderRules.Set = set
	settings.Values.HeaderRules.Remove = remove
	settings.Overrides = overrides
	settings.ReadOnly = readOnly
	return settings
}
