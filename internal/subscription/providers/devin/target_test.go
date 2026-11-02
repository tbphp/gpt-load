package devin

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type targetTransport func(*http.Request) (*http.Response, error)

func (transport targetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestDevinDiscoveryAndQuotaUseGroupTargetButRefreshUsesOfficialIdentity(t *testing.T) {
	var targets []string
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	http.DefaultTransport = targetTransport(func(request *http.Request) (*http.Response, error) {
		targets = append(targets, request.URL.String())
		if request.Method != "POST" || request.Header.Get("Authorization") != "Basic secret-secret" {
			t.Error("incorrect status request")
		}
		plan := protowire.AppendVarint(protowire.AppendTag(nil, 14, protowire.VarintType), 25)
		plan = protowire.AppendVarint(protowire.AppendTag(plan, 15, protowire.VarintType), 75)
		user := protowire.AppendBytes(protowire.AppendTag(nil, 36, protowire.BytesType), []byte("account"))
		user = protowire.AppendBytes(protowire.AppendTag(user, 13, protowire.BytesType), plan)
		raw := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), user)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw)), Request: request}, nil
	})
	driver := &devinDriver{}
	current, err := driver.Parse([]byte(`{"type":"devin","session_token":"secret","user_id":"account"}`))
	if err != nil {
		t.Fatal(err)
	}
	target := subscriptionruntime.NewTarget([]byte(`{"base_url":"https://relay.example/team"}`))
	models, err := (devinModelDiscovery{}).DiscoverModels(t.Context(), current, target)
	if err != nil || len(models) == 0 {
		t.Fatalf("model discovery: %v", err)
	}
	for _, model := range models {
		if strings.Contains(model, "/") {
			t.Fatalf("model cannot be used on Gemini routes: %s", model)
		}
	}
	observed, err := (devinQuotaObservation{}).Observe(t.Context(), current, target)
	if err != nil {
		t.Fatal(err)
	}
	if !observed.AccountObserved || !observed.QuotaObserved || observed.Partial || len(observed.ObservedQuotaScopes) != 2 {
		t.Fatal("quota coverage lost")
	}
	if _, err := driver.Refresh(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	path := "/exa.seat_management_pb.SeatManagementService/GetUserStatus"
	if len(targets) != 3 || targets[0] != "https://relay.example/team"+path || targets[1] != targets[0] || targets[2] != "https://server.codeium.com"+path {
		t.Fatalf("wrong target isolation: %v", targets)
	}
}
