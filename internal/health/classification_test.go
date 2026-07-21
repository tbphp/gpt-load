package health

import "testing"

func TestFailureCategoryZeroValueIsAmbiguous(t *testing.T) {
	var category FailureCategory
	if category != FailureCategoryAmbiguous {
		t.Fatalf("zero FailureCategory = %d, want ambiguous", category)
	}
}
