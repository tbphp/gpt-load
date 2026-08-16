package control

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
)

func TestParseModelPriceListQueryAcceptsFinalContract(t *testing.T) {
	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
		want       ModelPriceListQuery
	}{
		{
			name: "defaults",
			want: ModelPriceListQuery{
				Usage:  ModelPriceUsageInUse,
				Status: ModelPriceStatusAll,
				Page:   1, PageSize: 20,
			},
		},
		{
			name:     "all filters and trimmed search",
			rawQuery: "usage=unreferenced&status=pending&search=++GPT-5++&page=2&page_size=100",
			want: ModelPriceListQuery{
				Usage:  ModelPriceUsageUnreferenced,
				Status: ModelPriceStatusPending,
				Search: "GPT-5", Page: 2, PageSize: 100,
			},
		},
		{
			name:     "configured in-use",
			rawQuery: "usage=in_use&status=configured&page=1&page_size=1",
			want: ModelPriceListQuery{
				Usage:  ModelPriceUsageInUse,
				Status: ModelPriceStatusConfigured,
				Page:   1, PageSize: 1,
			},
		},
		{
			name:     "all usage and status",
			rawQuery: "usage=all&status=all&search=",
			want: ModelPriceListQuery{
				Usage:  ModelPriceUsageAll,
				Status: ModelPriceStatusAll,
				Page:   1, PageSize: 20,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, apiErr := parseModelPriceListQuery(test.rawQuery, test.forceQuery)
			if apiErr != nil {
				t.Fatalf("parseModelPriceListQuery() error = %v", apiErr)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseModelPriceListQuery() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseModelPriceListQueryRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
	}{
		{name: "force empty", forceQuery: true},
		{name: "unknown", rawQuery: "pattern=gpt"},
		{name: "repeated", rawQuery: "usage=all&usage=in_use"},
		{name: "empty usage", rawQuery: "usage="},
		{name: "invalid usage", rawQuery: "usage=referenced"},
		{name: "empty status", rawQuery: "status="},
		{name: "invalid status", rawQuery: "status=priced"},
		{name: "empty page", rawQuery: "page="},
		{name: "zero page", rawQuery: "page=0"},
		{name: "leading zero page", rawQuery: "page=01"},
		{name: "signed page", rawQuery: "page=%2B1"},
		{name: "negative page", rawQuery: "page=-1"},
		{name: "unsafe page", rawQuery: "page=9007199254740992"},
		{name: "empty page size", rawQuery: "page_size="},
		{name: "oversized page size", rawQuery: "page_size=101"},
		{name: "search too long", rawQuery: "search=" + strings.Repeat("界", 201)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, apiErr := parseModelPriceListQuery(test.rawQuery, test.forceQuery)
			if apiErr != app_errors.ErrBadRequest {
				t.Fatalf("parseModelPriceListQuery() error = %#v, want BAD_REQUEST", apiErr)
			}
		})
	}
}

func TestParseModelPriceRowIDRequiresCanonicalPositiveSafeUint(t *testing.T) {
	for _, value := range []string{"1", "9007199254740991"} {
		got, err := parseModelPriceRowID(value)
		if err != nil {
			t.Fatalf("parseModelPriceRowID(%q) error = %v", value, err)
		}
		if uint64(got) == 0 {
			t.Fatalf("parseModelPriceRowID(%q) = 0", value)
		}
	}

	for _, value := range []string{"", "0", "00", "01", "+1", "-1", "9007199254740992", "18446744073709551616"} {
		if _, err := parseModelPriceRowID(value); err == nil {
			t.Fatalf("parseModelPriceRowID(%q) accepted invalid ID", value)
		}
	}
}

func TestNullableDecimalAcceptsOnlyExactUSDStringOrNull(t *testing.T) {
	tests := []struct {
		body string
		want *int64
	}{
		{body: `null`},
		{body: `"0"`, want: int64Pointer(0)},
		{body: `"2.500000000"`, want: int64Pointer(2_500_000_000)},
		{body: `"9223372036.854775807"`, want: int64Pointer(math.MaxInt64)},
	}
	for _, test := range tests {
		var got nullableDecimal
		if err := json.Unmarshal([]byte(test.body), &got); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", test.body, err)
		}
		if !got.present || !pricePointerEqual(got.nanoUSD, test.want) {
			t.Fatalf("json.Unmarshal(%s) = %#v, want present value %v", test.body, got, test.want)
		}
	}
}

func TestNullableDecimalClassifiesInvalidDecimalStringsAsValidation(t *testing.T) {
	for _, body := range []string{
		`""`, `" "`, `"1e3"`, `"+1"`, `"-1"`, `".5"`, `"1."`,
		`"1.0000000000"`, `"9223372036.854775808"`,
	} {
		var got nullableDecimal
		err := json.Unmarshal([]byte(body), &got)
		if !errors.Is(err, app_errors.ErrValidation) {
			t.Fatalf("json.Unmarshal(%s) error = %v, want VALIDATION_FAILED cause", body, err)
		}
	}
}

func TestNullableDecimalRejectsJSONNumbersAndContainers(t *testing.T) {
	for _, body := range []string{`0`, `1.5`, `1e3`, `{}`, `[]`, `true`} {
		var got nullableDecimal
		if err := json.Unmarshal([]byte(body), &got); err == nil {
			t.Fatalf("json.Unmarshal(%s) accepted non-string price", body)
		}
	}
}

func TestModelPriceUpdateRequestRequiresFullReplacementSlots(t *testing.T) {
	request, apiErr := decodeModelPriceUpdateRequestForTest(
		`{"input":"2.5","output":null,"cache_read":"0","cache_write":null,"context_tiers":[],"mode_schedules":{}}`,
	)
	if apiErr != nil {
		t.Fatalf("decode request error = %v", apiErr)
	}
	if request.ConfirmUnpriced ||
		!pricePointerEqual(request.Input.nanoUSD, int64Pointer(2_500_000_000)) ||
		request.Output.nanoUSD != nil ||
		!pricePointerEqual(request.CacheRead.nanoUSD, int64Pointer(0)) ||
		request.CacheWrite.nanoUSD != nil ||
		len(request.ContextTiers.tiers) != 0 {
		t.Fatalf("decoded request = %#v", request)
	}

	confirmed, apiErr := decodeModelPriceUpdateRequestForTest(
		`{"input":null,"output":null,"cache_read":null,"cache_write":null,"confirm_unpriced":true,"context_tiers":[],"mode_schedules":{}}`,
	)
	if apiErr != nil || !confirmed.ConfirmUnpriced {
		t.Fatalf("confirmed all-null request = %#v, %v", confirmed, apiErr)
	}

	for _, body := range []string{
		`{}`,
		`{"input":"1","output":"2","cache_read":"3","context_tiers":[]}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4"}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[],"input":"5"}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[],"model_id":"secret"}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[],"price_scope_key":"provider:secret"}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[]} {}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":null}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[{"input":"1"}]}`,
		`{"input":"1","output":"2","cache_read":"3","cache_write":"4","context_tiers":[{"threshold_tokens":1000,"input":"1","extra":true}]}`,
		`[]`,
	} {
		_, apiErr := decodeModelPriceUpdateRequestForTest(body)
		if apiErr == nil {
			t.Fatalf("decode request accepted %s", body)
		}
	}
}

func TestModelPriceUpdateRequestDecodesContextTiers(t *testing.T) {
	request, apiErr := decodeModelPriceUpdateRequestForTest(
		`{"input":"2.5","output":null,"cache_read":"0","cache_write":null,"context_tiers":[` +
			`{"threshold_tokens":1000,"input":"3","output":null,"cache_read":null,"cache_write":null},` +
			`{"threshold_tokens":272000,"input":null,"output":"5","cache_read":null,"cache_write":null}` +
			`],"mode_schedules":{}}`,
	)
	if apiErr != nil {
		t.Fatalf("decode request error = %v", apiErr)
	}
	tiers := request.ContextTiers.tiers
	if len(tiers) != 2 ||
		!tiers[0].ThresholdTokens.present || tiers[0].ThresholdTokens.tokens != 1000 ||
		!pricePointerEqual(tiers[0].Input.nanoUSD, int64Pointer(3_000_000_000)) ||
		!tiers[1].ThresholdTokens.present || tiers[1].ThresholdTokens.tokens != 272_000 ||
		!pricePointerEqual(tiers[1].Output.nanoUSD, int64Pointer(5_000_000_000)) {
		t.Fatalf("decoded context tiers = %#v", tiers)
	}

	for _, body := range []string{
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[{"threshold_tokens":null,"input":"1"}]}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[{"threshold_tokens":-1,"input":"1"}]}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[{"threshold_tokens":1.5,"input":"1"}]}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[{"threshold_tokens":"1000","input":"1"}]}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[{"threshold_tokens":01,"input":"1"}]}`,
	} {
		_, apiErr := decodeModelPriceUpdateRequestForTest(body)
		if apiErr == nil {
			t.Fatalf("decode request accepted %s", body)
		}
	}
}

func TestModelPriceUpdateRequestDecodesModeSchedules(t *testing.T) {
	request, apiErr := decodeModelPriceUpdateRequestForTest(
		`{"input":"2","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],` +
			`"mode_schedules":{"fast":{"prices":{"input":"7","output":null,"cache_read":null,"cache_write":null},` +
			`"context_tiers":[]}}}`,
	)
	if apiErr != nil {
		t.Fatalf("decode request error = %v", apiErr)
	}
	fast, ok := request.ModeSchedules.schedules[pricing.ModeFast]
	if !ok || !pricePointerEqual(fast.Prices.Input.nanoUSD, int64Pointer(7_000_000_000)) ||
		len(fast.ContextTiers.tiers) != 0 {
		t.Fatalf("decoded fast schedule = %#v, %t", fast, ok)
	}

	for _, body := range []string{
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"mode_schedules":null}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"mode_schedules":{"standard":{"prices":{"input":"1","output":null,"cache_read":null,"cache_write":null},"context_tiers":[]}}}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"mode_schedules":{"fast":{"prices":{"input":null,"output":null,"cache_read":null,"cache_write":null},"context_tiers":[]}}}`,
		`{"input":"1","output":null,"cache_read":null,"cache_write":null,"context_tiers":[],"mode_schedules":{"fast":{"prices":{"input":"1","output":null,"cache_read":null,"cache_write":null},"context_tiers":[{"threshold_tokens":1000,"input":"2","output":null,"cache_read":null,"cache_write":null}]}}}`,
	} {
		if _, apiErr := decodeModelPriceUpdateRequestForTest(body); apiErr == nil {
			t.Fatalf("decode request accepted %s", body)
		}
	}
}

func TestModelPriceUpdateRequestClassifiesTypeAndDecimalErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode string
	}{
		{name: "number", input: `1`, wantCode: app_errors.ErrInvalidJSON.Code},
		{name: "exponent number", input: `1e3`, wantCode: app_errors.ErrInvalidJSON.Code},
		{name: "exponent string", input: `"1e3"`, wantCode: app_errors.ErrValidation.Code},
		{name: "negative string", input: `"-1"`, wantCode: app_errors.ErrValidation.Code},
		{name: "precision", input: `"1.0000000000"`, wantCode: app_errors.ErrValidation.Code},
		{name: "overflow", input: `"9223372036.854775808"`, wantCode: app_errors.ErrValidation.Code},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, apiErr := decodeModelPriceUpdateRequestForTest(
				`{"input":` + test.input + `,"output":null,"cache_read":null,"cache_write":null}`,
			)
			if apiErr == nil || apiErr.Code != test.wantCode {
				t.Fatalf("decode request error = %#v, want %s", apiErr, test.wantCode)
			}
		})
	}
}

func decodeModelPriceUpdateRequestForTest(
	body string,
) (ModelPriceUpdateRequest, *app_errors.APIError) {
	var request ModelPriceUpdateRequest
	if err := decodeStrictControlJSONObject([]byte(body), &request); err != nil {
		return ModelPriceUpdateRequest{}, mapControlJSONError(err)
	}
	if err := request.validate(); err != nil {
		return ModelPriceUpdateRequest{}, mapControlJSONError(err)
	}
	return request, nil
}

func int64Pointer(value int64) *int64 {
	return &value
}
