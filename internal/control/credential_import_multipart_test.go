package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/importfile"
)

func TestCredentialImportMultipartReportsTooManyFiles(t *testing.T) {
	t.Parallel()
	body, contentType := credentialImportMultipartBody(t, importfile.MaxEntries+1, 0, "account.json", false)
	response, _ := serveCredentialImportMultipart(t, body, contentType)
	assertCredentialImportLimitError(t, response, http.StatusBadRequest, "OAUTH_FILE_INVALID", "too_many_entries")
}

func TestCredentialImportMultipartAcceptsMaximumContentAndMetadata(t *testing.T) {
	t.Parallel()
	for _, filename := range []string{
		"codex-" + strings.Repeat("x", 72) + ".json",
		"codex-" + strings.Repeat("订阅", 100) + ".json",
	} {
		t.Run(fmt.Sprintf("filename-bytes-%d", len(filename)), func(t *testing.T) {
			t.Parallel()
			body, contentType := credentialImportMultipartBody(t, importfile.MaxEntries, importfile.MaxFileBytes, filename, true)
			response, fixture := serveCredentialImportMultipart(t, body, contentType)
			if response.Code != http.StatusOK {
				t.Fatalf("maximum legal content with multipart metadata returned %d, want 200", response.Code)
			}
			var result struct {
				Data CredentialImportBatchResult `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Data.Items) != importfile.MaxEntries {
				t.Fatalf("received %d items, want %d", len(result.Data.Items), importfile.MaxEntries)
			}
			for _, item := range result.Data.Items {
				if item.Status != "skipped" || item.ErrorCode != "already_prepared" {
					t.Fatalf("prepared item was not recognized: %s/%s", item.Status, item.ErrorCode)
				}
			}
			var count int64
			if err := fixture.db.Model(&models.CredentialStage{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("prepared inputs created stages: count=%d, error=%v", count, err)
			}
		})
	}
}

func TestCredentialImportMultipartStillRejectsExcessFileContent(t *testing.T) {
	t.Parallel()
	body, contentType := credentialImportMultipartBody(t, 1, importfile.MaxFileBytes+1, "account.json", false)
	response, _ := serveCredentialImportMultipart(t, body, contentType)
	assertCredentialImportLimitError(t, response, http.StatusRequestEntityTooLarge, "OAUTH_FILE_INVALID", "file_too_large")
}

func TestCredentialImportMultipartReportsRequestBodyLimit(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("channel_id", "codex"); err != nil {
		t.Fatal(err)
	}
	// 合成一个超过整包预算的 MIME 头，验证未知 Content-Length 时的流式限制。
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="account.json"`)
	header.Set("X-Padding", strings.Repeat("x", 9*1024*1024))
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	response, _ := serveCredentialImportMultipart(t, &body, writer.FormDataContentType())
	assertCredentialImportLimitError(t, response, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "")
}

func credentialImportMultipartBody(t *testing.T, fileCount, contentBytes int, filename string, metadata bool) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(strings.Repeat("b", 70)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("channel_id", "codex"); err != nil {
		t.Fatal(err)
	}
	if metadata {
		fingerprint := sha256.Sum256([]byte("codex\x00multipart-refresh"))
		ids := make([]string, importfile.MaxEntries)
		for index := range ids {
			ids[index] = hex.EncodeToString(fingerprint[:])
		}
		raw, err := json.Marshal(ids)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("prepared_import_ids", string(raw)+strings.Repeat(" ", 80*1024-len(raw))); err != nil {
			t.Fatal(err)
		}
		const proxy = `{"mode":"direct"}`
		if err := writer.WriteField("proxy", proxy+strings.Repeat(" ", 16*1024-len(proxy))); err != nil {
			t.Fatal(err)
		}
	}
	const credential = `{"type":"codex","access_token":"multipart-access","refresh_token":"multipart-refresh","account_id":"multipart-account","expired":"2035-01-01T00:00:00Z"}`
	for index := 0; index < fileCount; index++ {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		content := `{}`
		if contentBytes > 0 {
			size := contentBytes / fileCount
			if index < contentBytes%fileCount {
				size++
			}
			content = credential + strings.Repeat(" ", size-len(credential))
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func serveCredentialImportMultipart(t *testing.T, body *bytes.Buffer, contentType string) (*httptest.ResponseRecorder, serviceFixture) {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "multipart-test-auth"}, fixture.service).RegisterRoutes(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/import-batch", body)
	request.ContentLength = -1
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Authorization", "Bearer multipart-test-auth")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response, fixture
}

func assertCredentialImportLimitError(t *testing.T, response *httptest.ResponseRecorder, status int, code, importCode string) {
	t.Helper()
	var result struct {
		Code string `json:"code"`
		Data struct {
			ImportError string `json:"import_error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || result.Code != code || result.Data.ImportError != importCode {
		t.Fatalf("status/code/import_error = %d/%s/%s, want %d/%s/%s", response.Code, result.Code, result.Data.ImportError, status, code, importCode)
	}
}
