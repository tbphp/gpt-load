package control

import (
	"encoding/json"
	"net/url"
	"testing"

	"gpt-load/internal/requestlog"
)

func TestRequestLogClientIPQuery(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"192.0.2.1", "192.0.2.1"}, {"2001:0db8:0:0::1", "2001:db8::1"}, {"::ffff:192.0.2.1", "192.0.2.1"},
	} {
		query, err := parseRequestLogQuery("client_ip=" + url.QueryEscape(test.input))
		if err != nil || query.ClientIP != test.want {
			t.Fatalf("query=%+v, error=%v", query, err)
		}
	}
	for _, value := range []string{"", "192.0.2", "192.0.2.0/24", "192.0.2.1:80", "2001:db8::1%eth0", "192.0.2.1,192.0.2.2"} {
		if _, err := parseRequestLogQuery("client_ip=" + url.QueryEscape(value)); err == nil {
			t.Fatalf("accepted invalid IP %q", value)
		}
	}
	if _, err := parseRequestLogQuery("client_ip=192.0.2.1&client_ip=192.0.2.2"); err == nil {
		t.Fatal("duplicate filter accepted")
	}
}

func TestRequestLogClientIPResponse(t *testing.T) {
	for _, ip := range []string{"", "192.0.2.1", "2001:db8::1"} {
		response, err := mapRequestLogItemResponse(requestlog.Record{ClientIP: ip}, requestLogUsageCostResponse{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		value, present := fields["client_ip"]
		if !present || (ip == "" && value != nil) || (ip != "" && value != ip) {
			t.Fatalf("client_ip = %v, present = %t", value, present)
		}
	}
}
