package connection

import "testing"

func TestNormalizeUsesAPIKeyAsTheBackwardCompatibleDefault(t *testing.T) {
	for _, value := range []string{"", "  "} {
		if got := Normalize(value); got != APIKey {
			t.Fatalf("Normalize(%q) = %q, want %q", value, got, APIKey)
		}
	}
	if got := Normalize(" subscription "); got != Subscription {
		t.Fatalf("Normalize(subscription) = %q, want %q", got, Subscription)
	}
}

func TestValidAcceptsOnlyProductConnectionTypes(t *testing.T) {
	for _, value := range []string{"", APIKey, Subscription} {
		if !Valid(value) {
			t.Fatalf("Valid(%q) = false", value)
		}
	}
	if Valid("oauth") {
		t.Fatal("Valid(oauth) = true")
	}
}
