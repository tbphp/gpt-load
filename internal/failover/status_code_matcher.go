package failover

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// StatusCodeRange represents an inclusive HTTP status code interval [Start, End].
type StatusCodeRange struct {
	Start int
	End   int
}

// StatusCodeMatcher matches status codes against a compact, merged set of ranges.
// The zero value matches nothing.
type StatusCodeMatcher struct {
	ranges []StatusCodeRange
}

// Match returns true if code is within any configured range.
func (m StatusCodeMatcher) Match(code int) bool {
	if len(m.ranges) == 0 {
		return false
	}

	// Find first range whose Start is greater than code, then check previous one.
	i := sort.Search(len(m.ranges), func(i int) bool {
		return m.ranges[i].Start > code
	})
	if i == 0 {
		return false
	}
	r := m.ranges[i-1]
	return code >= r.Start && code <= r.End
}

// IsEmpty reports whether the matcher has no ranges configured.
func (m StatusCodeMatcher) IsEmpty() bool {
	return len(m.ranges) == 0
}

// ParseStatusCodeMatcher parses a comma-separated status code specification into a matcher.
//
// Spec grammar:
//   - Single code: "404"
//   - Inclusive range: "250-260"
//   - Multiple entries separated by commas: "404,429,500-599"
//
// Whitespace around tokens and around "-" is allowed.
// Empty tokens are ignored.
func ParseStatusCodeMatcher(spec string) (StatusCodeMatcher, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return StatusCodeMatcher{}, nil
	}

	// Allow users to paste multi-line values (treat newline as comma).
	spec = strings.ReplaceAll(spec, "\n", ",")

	tokens := strings.Split(spec, ",")
	parsed := make([]StatusCodeRange, 0, len(tokens))

	for _, raw := range tokens {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}

		if strings.Contains(token, "-") {
			parts := strings.Split(token, "-")
			if len(parts) != 2 {
				return StatusCodeMatcher{}, fmt.Errorf("invalid status code range: %q", token)
			}
			startStr := strings.TrimSpace(parts[0])
			endStr := strings.TrimSpace(parts[1])
			if startStr == "" || endStr == "" {
				return StatusCodeMatcher{}, fmt.Errorf("invalid status code range: %q", token)
			}

			start, err := strconv.Atoi(startStr)
			if err != nil {
				return StatusCodeMatcher{}, fmt.Errorf("invalid status code in range %q: %v", token, err)
			}
			end, err := strconv.Atoi(endStr)
			if err != nil {
				return StatusCodeMatcher{}, fmt.Errorf("invalid status code in range %q: %v", token, err)
			}
			if start > end {
				return StatusCodeMatcher{}, fmt.Errorf("invalid status code range (start > end): %q", token)
			}
			if !isValidStatusCode(start) || !isValidStatusCode(end) {
				return StatusCodeMatcher{}, fmt.Errorf("status code out of allowed range (100-999): %q", token)
			}

			parsed = append(parsed, StatusCodeRange{Start: start, End: end})
			continue
		}

		code, err := strconv.Atoi(token)
		if err != nil {
			return StatusCodeMatcher{}, fmt.Errorf("invalid status code: %q", token)
		}
		if !isValidStatusCode(code) {
			return StatusCodeMatcher{}, fmt.Errorf("status code out of allowed range (100-999): %q", token)
		}
		parsed = append(parsed, StatusCodeRange{Start: code, End: code})
	}

	if len(parsed) == 0 {
		return StatusCodeMatcher{}, nil
	}

	sort.Slice(parsed, func(i, j int) bool {
		if parsed[i].Start != parsed[j].Start {
			return parsed[i].Start < parsed[j].Start
		}
		return parsed[i].End < parsed[j].End
	})

	merged := make([]StatusCodeRange, 0, len(parsed))
	current := parsed[0]
	for _, r := range parsed[1:] {
		// Merge overlapping or adjacent ranges.
		if r.Start <= current.End+1 {
			if r.End > current.End {
				current.End = r.End
			}
			continue
		}
		merged = append(merged, current)
		current = r
	}
	merged = append(merged, current)

	return StatusCodeMatcher{ranges: merged}, nil
}

func isValidStatusCode(code int) bool {
	return code >= 100 && code <= 999
}
