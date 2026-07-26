package usage

import "math"

// SubtractCached derives uncached input tokens from an inclusive input total.
func SubtractCached(inclusive, cached int64) (int64, bool) {
	if inclusive < 0 || cached < 0 || cached > inclusive {
		return 0, false
	}
	return inclusive - cached, true
}

// CheckedAdd adds non-negative token counts without overflow.
func CheckedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

// CheckedTotal returns the sum of all token fields without overflow.
func CheckedTotal(tokens Tokens) (int64, bool) {
	total := int64(0)
	for _, value := range [...]int64{
		tokens.UncachedInput,
		tokens.CacheRead,
		tokens.CacheWrite5M,
		tokens.CacheWrite1H,
		tokens.Output,
	} {
		var ok bool
		total, ok = CheckedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}
