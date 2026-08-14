package health

import "testing"

func TestFailureCategoryZeroValueIsAmbiguous(t *testing.T) {
	var category FailureCategory
	if category != FailureCategoryAmbiguous {
		t.Fatalf("zero FailureCategory = %d, want ambiguous", category)
	}
}

func TestFailureCategoryStringIsStable(t *testing.T) {
	tests := map[FailureCategory]string{
		FailureCategoryAmbiguous:              "ambiguous",
		FailureCategoryOK:                     "ok",
		FailureCategoryRateLimited:            "rate_limited",
		FailureCategoryModelUnavailable:       "model_unavailable",
		FailureCategoryInvalidKey:             "invalid_key",
		FailureCategoryUpstreamHostError:      "upstream_host_error",
		FailureCategoryClientError:            "client_error",
		FailureCategoryDownstreamCancel:       "downstream_cancel",
		FailureCategoryAuthenticationRequired: "authentication_required",
	}
	for category, want := range tests {
		if got := category.String(); got != want {
			t.Fatalf("%v.String() = %q, want %q", category, got, want)
		}
	}
}
