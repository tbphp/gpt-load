package pricing

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

const nanoUSDPerUSD int64 = 1_000_000_000

// NanoUSD is a USD amount represented in billionths of one USD.
type NanoUSD int64

// Multiplier applies an exact positive ratio to a price component.
type Multiplier struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// ParseUSD parses a non-negative decimal USD amount with at most nine decimal places.
func ParseUSD(value string) (NanoUSD, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, errors.New("USD price must be a non-empty decimal")
	}

	integer, fraction, hasFraction := strings.Cut(value, ".")
	if integer == "" || (hasFraction && fraction == "") || strings.Contains(fraction, ".") || len(fraction) > 9 {
		return 0, errors.New("USD price must have at most nine decimal places")
	}
	if !decimalDigits(integer) || (hasFraction && !decimalDigits(fraction)) {
		return 0, errors.New("USD price must be a non-negative decimal")
	}

	amount := new(big.Int)
	if _, ok := amount.SetString(integer+fraction+strings.Repeat("0", 9-len(fraction)), 10); !ok || !amount.IsInt64() {
		return 0, errors.New("USD price exceeds int64 nano USD")
	}
	return NanoUSD(amount.Int64()), nil
}

// FormatUSD formats a NanoUSD amount as a plain decimal USD string.
func FormatUSD(value NanoUSD) string {
	amount := big.NewInt(int64(value))
	negative := amount.Sign() < 0
	if negative {
		amount.Abs(amount)
	}

	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(amount, big.NewInt(nanoUSDPerUSD), fraction)
	if fraction.Sign() == 0 {
		if negative {
			return "-" + whole.String()
		}
		return whole.String()
	}

	fractionText := strconv.FormatInt(fraction.Int64(), 10)
	fractionText = strings.Repeat("0", 9-len(fractionText)) + fractionText
	fractionText = strings.TrimRight(fractionText, "0")
	prefix := ""
	if negative {
		prefix = "-"
	}
	return prefix + whole.String() + "." + fractionText
}

// QuoteComponent returns the rounded nano USD cost of one usage component.
func QuoteComponent(tokens int64, price NanoUSD, multiplier Multiplier) (NanoUSD, bool) {
	if tokens < 0 || price < 0 || multiplier.Numerator <= 0 || multiplier.Denominator <= 0 {
		return 0, false
	}

	numerator := big.NewInt(tokens)
	numerator.Mul(numerator, big.NewInt(int64(price)))
	numerator.Mul(numerator, big.NewInt(multiplier.Numerator))
	denominator := big.NewInt(tokensPerMillion)
	denominator.Mul(denominator, big.NewInt(multiplier.Denominator))

	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	remainder.Lsh(remainder, 1)
	if remainder.Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if quotient.Sign() < 0 || !quotient.IsInt64() {
		return 0, false
	}
	return NanoUSD(quotient.Int64()), true
}

// CheckedAddNanoUSD adds non-negative NanoUSD values without overflow.
func CheckedAddNanoUSD(left, right NanoUSD) (NanoUSD, bool) {
	if left < 0 || right < 0 || left > NanoUSD(math.MaxInt64)-right {
		return 0, false
	}
	return left + right, true
}

func decimalDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
