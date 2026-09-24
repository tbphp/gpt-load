package codexrouting

import "testing"

func TestCandyPassedIgnoresTokenCounts(t *testing.T) {
	t.Parallel()
	if candyPassed([]byte(`data: {"type":"response.completed","response":{"usage":{"output_tokens":721}}}

`)) {
		t.Fatal("token count 721 should not pass")
	}
	if !candyPassed([]byte(`data: {"type":"response.output_text.delta","delta":"最少取出 21 个"}

`)) {
		t.Fatal("answer 21 should pass")
	}
	if candyPassed([]byte(`data: {"type":"response.output_text.delta","delta":"最少取出 29 个"}

`)) {
		t.Fatal("answer 29 should fail")
	}
}
