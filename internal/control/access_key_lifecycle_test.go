package control

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyLifecycleCreatePersistsCanonicalPolicyAndKeepsWireArrays(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(append(
		bytes.Repeat([]byte{0x11}, 16),
		bytes.Repeat([]byte{0x12}, 16)...,
	))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	engine := newAccessKeyLifecycleEngine(t, fixture)
	expiresAtMS := operationNow.Add(time.Hour).UnixMilli()

	created := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		fmt.Sprintf(`{
			"name":"restricted",
			"expires_at_ms":%d,
			"filters":{
				"groups":[],
				"protocols":[],
				"models":[],
				"allowed_cidrs":[" 192.0.2.42/24 ","192.0.2.1","192.0.2.1/32"]
			}
		}`, expiresAtMS),
		"00000000-0000-4000-8000-000000007001",
	)
	if created.Code != http.StatusOK {
		t.Fatalf("create restricted = %d %s", created.Code, created.Body.String())
	}
	createdData := decodeAccessKeyLifecycleData(t, created)
	assertJSONRawEqual(t, createdData["expires_at_ms"], strconv.FormatInt(expiresAtMS, 10))
	var createdFilters map[string]json.RawMessage
	if err := json.Unmarshal(createdData["filters"], &createdFilters); err != nil {
		t.Fatalf("decode created filters: %v", err)
	}
	var allowedCIDRs []string
	if err := json.Unmarshal(createdFilters["allowed_cidrs"], &allowedCIDRs); err != nil {
		t.Fatalf("decode created allowed_cidrs: %v", err)
	}
	if want := []string{"192.0.2.0/24", "192.0.2.1/32"}; !reflect.DeepEqual(allowedCIDRs, want) {
		t.Fatalf("allowed_cidrs = %#v, want %#v", allowedCIDRs, want)
	}
	var createdID uint
	if err := json.Unmarshal(createdData["id"], &createdID); err != nil {
		t.Fatalf("decode created id: %v", err)
	}
	row := loadAccessKeyRow(t, fixture.db, createdID)
	if row.ExpiresAtMS == nil || *row.ExpiresAtMS != expiresAtMS {
		t.Fatalf("stored expires_at_ms = %#v, want %d", row.ExpiresAtMS, expiresAtMS)
	}
	if !strings.Contains(string(row.Filters), `"allowed_cidrs":["192.0.2.0/24","192.0.2.1/32"]`) {
		t.Fatalf("stored filters = %s", row.Filters)
	}

	unrestricted := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		`{"name":"unrestricted"}`,
		"00000000-0000-4000-8000-000000007002",
	)
	if unrestricted.Code != http.StatusOK {
		t.Fatalf("create unrestricted = %d %s", unrestricted.Code, unrestricted.Body.String())
	}
	unrestrictedData := decodeAccessKeyLifecycleData(t, unrestricted)
	assertJSONRawEqual(t, unrestrictedData["expires_at_ms"], "null")
	var unrestrictedFilters map[string]json.RawMessage
	if err := json.Unmarshal(unrestrictedData["filters"], &unrestrictedFilters); err != nil {
		t.Fatalf("decode unrestricted filters: %v", err)
	}
	assertJSONRawEqual(t, unrestrictedFilters["allowed_cidrs"], "[]")
	var unrestrictedID uint
	if err := json.Unmarshal(unrestrictedData["id"], &unrestrictedID); err != nil {
		t.Fatalf("decode unrestricted id: %v", err)
	}
	unrestrictedRow := loadAccessKeyRow(t, fixture.db, unrestrictedID)
	if unrestrictedRow.ExpiresAtMS != nil || strings.Contains(string(unrestrictedRow.Filters), "allowed_cidrs") {
		t.Fatalf("unrestricted persisted policy = expires:%#v filters:%s", unrestrictedRow.ExpiresAtMS, unrestrictedRow.Filters)
	}
}

func TestAccessKeyLifecycleCreateValidatesSafeFutureTimeWithoutBreakingReplay(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x22}, 32))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	engine := newAccessKeyLifecycleEngine(t, fixture)

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "equal to operation time", value: strconv.FormatInt(operationNow.UnixMilli(), 10)},
		{name: "before operation time", value: strconv.FormatInt(operationNow.Add(-time.Millisecond).UnixMilli(), 10)},
		{name: "outside JavaScript safe range", value: "9007199254740992"},
		{name: "fractional milliseconds", value: "1788000000000.5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeRevision := fixture.manager.Current().Revision
			recorder := serveAccessKeyLifecycleRequest(
				t,
				engine,
				http.MethodPost,
				"/api/access-keys",
				`{"name":"invalid-expiry","expires_at_ms":`+test.value+`}`,
				"00000000-0000-4000-8000-00000000701"+strconv.Itoa(len(test.name)%10),
			)
			if recorder.Code != http.StatusBadRequest ||
				test.name != "fractional milliseconds" && !strings.Contains(recorder.Body.String(), "VALIDATION_FAILED") {
				t.Fatalf("create invalid expiry = %d %s", recorder.Code, recorder.Body.String())
			}
			if fixture.manager.Current().Revision != beforeRevision {
				t.Fatalf("invalid expiry published revision %d, want %d", fixture.manager.Current().Revision, beforeRevision)
			}
		})
	}

	const idempotencyKey = "00000000-0000-4000-8000-000000007020"
	expiresAtMS := operationNow.Add(time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"name":"replay-across-expiry","expires_at_ms":%d}`, expiresAtMS)
	first := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", body, idempotencyKey)
	if first.Code != http.StatusOK {
		t.Fatalf("first create = %d %s", first.Code, first.Body.String())
	}
	operationNow = operationNow.Add(2 * time.Hour)
	replayed := serveAccessKeyLifecycleRequest(t, engine, http.MethodPost, "/api/access-keys", body, idempotencyKey)
	if replayed.Code != http.StatusOK {
		t.Fatalf("expired replay = %d %s", replayed.Code, replayed.Body.String())
	}
	firstData := decodeAccessKeyLifecycleData(t, first)
	replayedData := decodeAccessKeyLifecycleData(t, replayed)
	assertJSONRawEqual(t, firstData["replayed"], "false")
	assertJSONRawEqual(t, replayedData["replayed"], "true")
	assertJSONRawEqual(t, replayedData["id"], string(firstData["id"]))
}

func TestAccessKeyLifecycleDefaultCreatePreservesV1DigestAndNullEquivalence(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x33}, 16))
	fixture.service.now = func() time.Time {
		return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	}
	engine := newAccessKeyLifecycleEngine(t, fixture)
	const idempotencyKey = "00000000-0000-4000-8000-000000007030"

	first := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		`{"name":"CI client"}`,
		idempotencyKey,
	)
	if first.Code != http.StatusOK {
		t.Fatalf("default create = %d %s", first.Code, first.Body.String())
	}
	replayed := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		`{"name":"CI client","expires_at_ms":null,"filters":{"groups":[],"protocols":[],"models":[],"allowed_cidrs":[]}}`,
		idempotencyKey,
	)
	if replayed.Code != http.StatusOK {
		t.Fatalf("default-equivalent replay = %d %s", replayed.Code, replayed.Body.String())
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, replayed)["replayed"], "true")

	var operation models.ControlOperation
	if err := fixture.db.Where("idempotency_key = ?", idempotencyKey).Take(&operation).Error; err != nil {
		t.Fatalf("load create operation: %v", err)
	}
	// This is the digest produced by the pre-0007 create path. Its canonical
	// filter body uses null for normalized empty route slices, so it is distinct
	// from the standalone fixture that intentionally exercises explicit arrays.
	const wantDigestHex = "bb76f778ec3bc50726d131ea6292f3efb355da3ebab40c7e21f78a51aa2d7dac"
	if got := hex.EncodeToString(operation.RequestDigest); got != wantDigestHex {
		t.Fatalf("default create digest = %s, want %s", got, wantDigestHex)
	}
}

func TestAccessKeyLifecycleUpdateDistinguishesOmittedNullAndValue(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x44}, 16))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	engine := newAccessKeyLifecycleEngine(t, fixture)
	initialExpiry := operationNow.Add(time.Hour).UnixMilli()

	created := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		fmt.Sprintf(`{"name":"update-expiry","expires_at_ms":%d}`, initialExpiry),
		"00000000-0000-4000-8000-000000007040",
	)
	if created.Code != http.StatusOK {
		t.Fatalf("seed create = %d %s", created.Code, created.Body.String())
	}
	createdData := decodeAccessKeyLifecycleData(t, created)
	var id uint
	if err := json.Unmarshal(createdData["id"], &id); err != nil {
		t.Fatal(err)
	}
	path := "/api/access-keys/" + strconv.FormatUint(uint64(id), 10)

	omitted := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, `{"name":"renamed"}`, "")
	if omitted.Code != http.StatusOK {
		t.Fatalf("omitted update = %d %s", omitted.Code, omitted.Body.String())
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, omitted)["expires_at_ms"], strconv.FormatInt(initialExpiry, 10))

	cleared := serveAccessKeyLifecycleRequest(t, engine, http.MethodPut, path, `{"expires_at_ms":null}`, "")
	if cleared.Code != http.StatusOK {
		t.Fatalf("null update = %d %s", cleared.Code, cleared.Body.String())
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, cleared)["expires_at_ms"], "null")
	if row := loadAccessKeyRow(t, fixture.db, id); row.ExpiresAtMS != nil {
		t.Fatalf("null update stored expiry = %#v", row.ExpiresAtMS)
	}

	updatedExpiry := operationNow.Add(2 * time.Hour).UnixMilli()
	set := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPut,
		path,
		`{"expires_at_ms":`+strconv.FormatInt(updatedExpiry, 10)+`}`,
		"",
	)
	if set.Code != http.StatusOK {
		t.Fatalf("value update = %d %s", set.Code, set.Body.String())
	}
	assertJSONRawEqual(t, decodeAccessKeyLifecycleData(t, set)["expires_at_ms"], strconv.FormatInt(updatedExpiry, 10))
	if row := loadAccessKeyRow(t, fixture.db, id); row.ExpiresAtMS == nil || *row.ExpiresAtMS != updatedExpiry {
		t.Fatalf("value update stored expiry = %#v", row.ExpiresAtMS)
	}

	beforeRevision := fixture.manager.Current().Revision
	rejected := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPut,
		path,
		`{"expires_at_ms":`+strconv.FormatInt(operationNow.UnixMilli(), 10)+`}`,
		"",
	)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "VALIDATION_FAILED") {
		t.Fatalf("past update = %d %s", rejected.Code, rejected.Body.String())
	}
	if row := loadAccessKeyRow(t, fixture.db, id); row.ExpiresAtMS == nil || *row.ExpiresAtMS != updatedExpiry {
		t.Fatalf("rejected update changed expiry = %#v", row.ExpiresAtMS)
	}
	if fixture.manager.Current().Revision != beforeRevision {
		t.Fatalf("rejected update published revision %d, want %d", fixture.manager.Current().Revision, beforeRevision)
	}
}

func TestAccessKeyCollectionDerivesExpiredFromOneServerObservation(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x55}, 16))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	engine := newAccessKeyLifecycleEngine(t, fixture)
	expiresAtMS := operationNow.Add(time.Hour).UnixMilli()
	created := serveAccessKeyLifecycleRequest(
		t,
		engine,
		http.MethodPost,
		"/api/access-keys",
		fmt.Sprintf(`{"name":"expires-for-list","expires_at_ms":%d}`, expiresAtMS),
		"00000000-0000-4000-8000-000000007050",
	)
	if created.Code != http.StatusOK {
		t.Fatalf("seed create = %d %s", created.Code, created.Body.String())
	}
	operationNow = operationNow.Add(time.Hour)

	listed := serveAccessKeyLifecycleRequest(t, engine, http.MethodGet, "/api/access-keys", "", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	var envelope struct {
		Data struct {
			Summary AccessKeyCollectionSummary   `json:"summary"`
			Items   []map[string]json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if envelope.Data.Summary.Active != 1 || len(envelope.Data.Items) != 1 {
		t.Fatalf("list data = %#v", envelope.Data)
	}
	assertJSONRawEqual(t, envelope.Data.Items[0]["expired"], "true")
	assertJSONRawEqual(t, envelope.Data.Items[0]["expires_at_ms"], strconv.FormatInt(expiresAtMS, 10))
}

func TestAccessKeyHomeCurrentAccessKeyIncludesLifecyclePolicy(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(bytes.Repeat([]byte{0x56}, 16))
	operationNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return operationNow }
	expiresAtMS := operationNow.Add(time.Hour).UnixMilli()
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name:        "home-lifecycle",
		ExpiresAtMS: &expiresAtMS,
		Filters: &AccessKeyFilters{
			AllowedCIDRs: []string{"192.0.2.0/24"},
		},
	})
	if err != nil {
		t.Fatalf("seed AccessKey: %v", err)
	}

	base, err := fixture.service.ReadAccessKeyHomeBase(
		t.Context(),
		operationNow.UnixMilli(),
		created.ID,
	)
	if err != nil {
		t.Fatalf("ReadAccessKeyHomeBase() error = %v", err)
	}
	current := base.CurrentAccessKey
	if current == nil || current.ExpiresAtMS == nil || *current.ExpiresAtMS != expiresAtMS || current.Expired {
		t.Fatalf("current AccessKey lifecycle = %#v", current)
	}
	if want := []string{"192.0.2.0/24"}; !reflect.DeepEqual(current.Filters.AllowedCIDRs, want) {
		t.Fatalf("current allowed_cidrs = %#v, want %#v", current.Filters.AllowedCIDRs, want)
	}
}

func newAccessKeyLifecycleEngine(t *testing.T, fixture serviceFixture) *gin.Engine {
	t.Helper()
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	return engine
}

func serveAccessKeyLifecycleRequest(
	t *testing.T,
	engine *gin.Engine,
	method string,
	path string,
	body string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	request.Header.Set("Accept-Language", "en-US")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeAccessKeyLifecycleData(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode AccessKey response: %v", err)
	}
	if envelope.Data == nil {
		t.Fatalf("AccessKey response has no data: %s", recorder.Body.String())
	}
	return envelope.Data
}

func assertJSONRawEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if string(got) != want {
		t.Fatalf("JSON field = %s, want %s", got, want)
	}
}
