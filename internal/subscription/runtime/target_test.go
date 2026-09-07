package subscriptionruntime

import "testing"

func TestTargetBaseURL(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
		fail bool
	}{
		{"", "", false},
		{`{}`, "", false},
		{`{"base_url":""}`, "", false},
		{`{"base_url":"HTTPS://RELAY.EXAMPLE:443/team-a/"}`, "https://relay.example/team-a", false},
		{`{"base_url":"http://relay.example"}`, "", true},
		{`{"base_url":"https://relay.example?query=value"}`, "", true},
		{`{"base_url":7}`, "", true},
		{`invalid`, "", true},
	} {
		t.Run(test.raw, func(t *testing.T) {
			got, err := NewTarget([]byte(test.raw)).BaseURL()
			if got != test.want || (err != nil) != test.fail {
				t.Fatalf("BaseURL() = %q, %v; want %q, failure=%t", got, err, test.want, test.fail)
			}
		})
	}
}
