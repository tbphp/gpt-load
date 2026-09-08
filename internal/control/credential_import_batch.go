package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/subscription/providers/importfile"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// 格式信息只属于本次导入结果，不进入 Stage 或持久凭据。
type CredentialImportBatchItem struct {
	FileIndex int                    `json:"file_index"`
	Index     int                    `json:"index"`
	ImportID  string                 `json:"import_id,omitempty"`
	Format    string                 `json:"format,omitempty"`
	ChannelID channel.ID             `json:"channel_id,omitempty"`
	Status    string                 `json:"status"`
	Stage     *CredentialStageResult `json:"stage,omitempty"`
	ErrorCode string                 `json:"error_code,omitempty"`
}

type CredentialImportBatchResult struct {
	Items []CredentialImportBatchItem `json:"items"`
}

func normalizedSingleCredentialImport(channelID channel.ID, raw []byte, driver subscriptionruntime.Driver) ([]byte, error) {
	document, err := importfile.Parse(raw)
	if err != nil {
		return nil, app_errors.ErrOAuthFileInvalid
	}
	defer clearImportDocument(document)
	if len(document.Entries) != 1 {
		return nil, app_errors.ErrOAuthFileInvalid
	}
	entry := document.Entries[0]
	if entry.ErrorCode != "" || entry.ChannelID != channelID {
		return nil, app_errors.ErrOAuthFileInvalid
	}
	if err := validateExistingCPAImport(entry, driver); err != nil {
		return nil, err
	}
	return append([]byte(nil), entry.Credential...), nil
}

// ImportCredentialBatch 逐项暂存已选渠道，其他渠道和无效条目保留可定位结果。
// 同一批次串行处理，避免重复 Refresh Token 被并发轮换；整个批次有统一截止时间。
func (s *Service) ImportCredentialBatch(
	ctx context.Context,
	channelID channel.ID,
	raw []byte,
	groupID uint,
	proxyConfig *outboundproxy.Config,
) (CredentialImportBatchResult, error) {
	return s.ImportCredentialFiles(ctx, channelID, [][]byte{raw}, groupID, proxyConfig, nil)
}

func (s *Service) ImportCredentialFiles(
	ctx context.Context,
	channelID channel.ID,
	files [][]byte,
	groupID uint,
	proxyConfig *outboundproxy.Config,
	preparedIDs []string,
) (CredentialImportBatchResult, error) {
	driver, err := s.credentialStageImportDriver(channelID, nil)
	if err != nil {
		return CredentialImportBatchResult{}, err
	}
	if len(files) == 0 || len(files) > importfile.MaxEntries || len(preparedIDs) > importfile.MaxEntries {
		return CredentialImportBatchResult{}, credentialImportDocumentError(&importfile.Error{Code: "too_many_entries"})
	}
	prepared := make(map[string]struct{}, len(preparedIDs))
	for _, id := range preparedIDs {
		decoded, decodeErr := hex.DecodeString(id)
		if decodeErr != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != id {
			return CredentialImportBatchResult{}, app_errors.ErrOAuthFileInvalid
		}
		prepared[id] = struct{}{}
	}
	type fileEntry struct {
		importfile.Entry
		FileIndex int
	}
	entries := make([]fileEntry, 0)
	defer func() {
		for _, entry := range entries {
			clear(entry.Credential)
		}
	}()
	totalBytes := 0
	for fileIndex, raw := range files {
		totalBytes += len(raw)
		if totalBytes > importfile.MaxFileBytes {
			return CredentialImportBatchResult{}, credentialImportDocumentError(&importfile.Error{Code: "file_too_large"})
		}
		document, parseErr := importfile.Parse(raw)
		if parseErr != nil {
			if len(files) == 1 {
				return CredentialImportBatchResult{}, credentialImportDocumentError(parseErr)
			}
			var importErr *importfile.Error
			code := "invalid_json"
			if errors.As(parseErr, &importErr) {
				code = importErr.Code
			}
			entries = append(entries, fileEntry{Entry: importfile.Entry{Index: 1, ErrorCode: code}, FileIndex: fileIndex + 1})
		} else {
			for _, entry := range document.Entries {
				entries = append(entries, fileEntry{Entry: entry, FileIndex: fileIndex + 1})
			}
		}
		if len(entries) > importfile.MaxEntries {
			return CredentialImportBatchResult{}, credentialImportDocumentError(&importfile.Error{Code: "too_many_entries"})
		}
	}
	var network subscriptionruntime.NetworkContext
	if groupID != 0 {
		if proxyConfig != nil {
			return CredentialImportBatchResult{}, app_errors.ErrValidation
		}
		network, err = s.groupCredentialStageNetworkContext(ctx, groupID, channelID)
	} else {
		network, err = s.draftNetworkContext(ctx, proxyConfig)
	}
	if err != nil {
		return CredentialImportBatchResult{}, err
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	ctx, cancel := credentialImportContext(ctx)
	defer cancel()
	result := CredentialImportBatchResult{Items: make([]CredentialImportBatchItem, 0, len(entries))}
	identities := make(map[string]struct{})
	attempts := make(map[[32]byte]CredentialImportBatchItem)
	for _, entry := range entries {
		item := CredentialImportBatchItem{
			FileIndex: entry.FileIndex, Index: entry.Index, Format: entry.Format, ChannelID: entry.ChannelID,
			Status: "failed", ErrorCode: entry.ErrorCode,
		}
		var attempted bool
		var inputHash [32]byte
		switch {
		case entry.ChannelID != "" && entry.ChannelID != channelID:
			item.Status, item.ErrorCode = "skipped", "channel_mismatch"
		case entry.ErrorCode != "":
			// 格式错误已由解析器归类，不能再次尝试另一种认证方式。
		default:
			if err := validateExistingCPAImport(entry.Entry, driver); err != nil {
				item.ErrorCode = "invalid_credential"
				break
			}
			inputHash = credentialImportTokenFingerprint(channelID, entry.Credential)
			item.ImportID = hex.EncodeToString(inputHash[:])
			if _, exists := prepared[item.ImportID]; exists {
				item.Status, item.ErrorCode = "skipped", "already_prepared"
				break
			}
			if previous, duplicate := attempts[inputHash]; duplicate {
				if previous.Status == "failed" {
					item.ErrorCode = previous.ErrorCode
				} else {
					item.Status, item.ErrorCode = "skipped", "duplicate_account"
				}
				break
			}
			if ctx.Err() != nil {
				item.ErrorCode = "import_timeout"
				break
			}
			attempted = true
			credential, importErr := s.subscriptions.ImportCredential(ctx, channelID, entry.Credential)
			if importErr != nil {
				item.ErrorCode = credentialImportItemError(ctx, classifyCredentialImportError(driver, importErr))
				break
			}
			if _, duplicate := identities[credential.Identity()]; duplicate {
				item.Status, item.ErrorCode = "skipped", "duplicate_account"
				break
			}
			credential, importErr = s.prepareTransientSubscriptionCredential(ctx, channelID, driver, credential)
			if importErr != nil {
				item.ErrorCode = credentialImportItemError(ctx, importErr)
				break
			}
			stage, persistErr := s.persistReadyCredentialStage(ctx, channelID, "oauth_file", credential, network)
			if persistErr != nil {
				item.ErrorCode = credentialImportItemError(ctx, persistErr)
				break
			}
			identities[credential.Identity()] = struct{}{}
			item.Status, item.Stage = "ready", &stage
		}
		if attempted {
			attempts[inputHash] = item
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// 新格式允许补全字段；原 CPA 合同继续保留原先的完整性校验。
func validateExistingCPAImport(entry importfile.Entry, driver subscriptionruntime.Driver) error {
	if entry.Format != "cpa" || (entry.ChannelID != channel.Codex && entry.ChannelID != channel.Claude) {
		return nil
	}
	if _, err := driver.Parse(entry.Credential); err != nil {
		return app_errors.ErrOAuthFileInvalid
	}
	return nil
}

// 不同格式可能带有同一份授权；即使前一次刷新结果不确定，也不能重用旧 RT。
func credentialImportTokenFingerprint(channelID channel.ID, raw []byte) [32]byte {
	var value struct {
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value.RefreshToken) == "" {
		return sha256.Sum256(raw)
	}
	return sha256.Sum256([]byte(string(channelID) + "\x00" + strings.TrimSpace(value.RefreshToken)))
}

func clearImportDocument(document importfile.Document) {
	for _, entry := range document.Entries {
		clear(entry.Credential)
	}
}

func credentialImportDocumentError(err error) error {
	code := "invalid_json"
	var importErr *importfile.Error
	if errors.As(err, &importErr) {
		code = importErr.Code
	}
	status := http.StatusBadRequest
	if code == "file_too_large" {
		status = http.StatusRequestEntityTooLarge
	}
	return &app_errors.APIError{
		HTTPStatus: status, Code: app_errors.ErrOAuthFileInvalid.Code,
		Message: app_errors.ErrOAuthFileInvalid.Message,
		Data:    map[string]string{"import_error": code},
	}
}

func credentialImportItemError(ctx context.Context, err error) string {
	// 内层可能已经把请求超时转换为上游或授权错误，批次截止状态优先。
	if ctx.Err() != nil {
		return "import_timeout"
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "import_timeout"
	case errors.Is(err, app_errors.ErrCredentialReauthorizationRequired):
		return "reauthorization_required"
	case errors.Is(err, app_errors.ErrCredentialAuthOutcomeUnknown):
		return "authorization_unknown"
	case errors.Is(err, app_errors.ErrBadGateway), errors.Is(err, app_errors.ErrCredentialRefreshTemporarilyUnavailable):
		return "upstream_unavailable"
	case errors.Is(err, app_errors.ErrOAuthFileInvalid):
		return "invalid_credential"
	default:
		return "import_failed"
	}
}

func classifyCredentialImportError(driver subscriptionruntime.Driver, err error) error {
	decision := driver.ClassifyRefreshFailure(err)
	switch decision.Kind {
	case subscriptionruntime.RefreshFailureRetryable:
		return app_errors.ErrCredentialRefreshTemporarilyUnavailable
	case subscriptionruntime.RefreshFailureReauthorizationRequired, subscriptionruntime.RefreshFailureIdentityChanged:
		return app_errors.ErrCredentialReauthorizationRequired
	default:
		if decision.StatusCode != 0 {
			return app_errors.ErrCredentialAuthOutcomeUnknown
		}
		return credentialImportAPIError(err)
	}
}
