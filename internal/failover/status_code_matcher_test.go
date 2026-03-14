package failover

import "testing"

func TestParseStatusCodeMatcher_Empty(t *testing.T) {
	m, err := ParseStatusCodeMatcher("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsEmpty() {
		t.Fatalf("expected empty matcher")
	}
	if m.Match(404) {
		t.Fatalf("empty matcher should not match")
	}
}

func TestParseStatusCodeMatcher_SingleCode(t *testing.T) {
	m, err := ParseStatusCodeMatcher("404")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Match(404) {
		t.Fatalf("expected to match 404")
	}
	if m.Match(403) {
		t.Fatalf("did not expect to match 403")
	}
}

func TestParseStatusCodeMatcher_Range(t *testing.T) {
	m, err := ParseStatusCodeMatcher("250-260")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, code := range []int{250, 255, 260} {
		if !m.Match(code) {
			t.Fatalf("expected to match %d", code)
		}
	}
	for _, code := range []int{249, 261} {
		if m.Match(code) {
			t.Fatalf("did not expect to match %d", code)
		}
	}
}

func TestParseStatusCodeMatcher_MergeAdjacent(t *testing.T) {
	m, err := ParseStatusCodeMatcher("250-260,261")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, code := range []int{250, 260, 261} {
		if !m.Match(code) {
			t.Fatalf("expected to match %d", code)
		}
	}
	if m.Match(262) {
		t.Fatalf("did not expect to match 262")
	}
}

func TestParseStatusCodeMatcher_MergeOverlappingAndWhitespace(t *testing.T) {
	m, err := ParseStatusCodeMatcher(" 404 , 500-550 , 540-599 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Match(404) || !m.Match(500) || !m.Match(575) || !m.Match(599) {
		t.Fatalf("expected matches for configured codes")
	}
	if m.Match(400) || m.Match(600) {
		t.Fatalf("did not expect matches outside configured codes")
	}
}

func TestParseStatusCodeMatcher_InvalidSpecs(t *testing.T) {
	cases := []string{
		"abc",
		"99",
		"1000",
		"250-",
		"-260",
		"260-250",
		"250-260-270",
	}
	for _, c := range cases {
		if _, err := ParseStatusCodeMatcher(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}
