package utils

import "strconv"

const nanoUSDPerUSD int64 = 1_000_000_000

// FormatDurationMS formats a millisecond duration for human-readable runtime logs.
func FormatDurationMS(milliseconds int64) string {
	if milliseconds < 0 {
		return "?"
	}
	if milliseconds < 1_000 {
		return strconv.FormatInt(milliseconds, 10) + "ms"
	}
	if milliseconds < 60_000 {
		return formatSubminuteDuration(milliseconds)
	}

	totalSeconds := milliseconds / 1_000
	if milliseconds%1_000 >= 500 {
		totalSeconds++
	}
	hours := totalSeconds / 3_600
	minutes := (totalSeconds % 3_600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return strconv.FormatInt(hours, 10) + "h" + twoDigits(minutes) + "m" + twoDigits(seconds) + "s"
	}
	return strconv.FormatInt(minutes, 10) + "m" + twoDigits(seconds) + "s"
}

func formatSubminuteDuration(milliseconds int64) string {
	if milliseconds < 10_000 {
		hundredths := (milliseconds + 5) / 10
		whole := hundredths / 100
		fraction := hundredths % 100
		return formatDecimalSeconds(whole, fraction, 2)
	}

	tenths := (milliseconds + 50) / 100
	whole := tenths / 10
	fraction := tenths % 10
	return formatDecimalSeconds(whole, fraction, 1)
}

func formatDecimalSeconds(whole, fraction int64, fractionDigits int) string {
	if fraction == 0 {
		return strconv.FormatInt(whole, 10) + "s"
	}
	fractionText := strconv.FormatInt(fraction, 10)
	for len(fractionText) < fractionDigits {
		fractionText = "0" + fractionText
	}
	return strconv.FormatInt(whole, 10) + "." + fractionText + "s"
}

func twoDigits(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}

// FormatNanoUSD converts an integer nano-USD amount to a compact decimal string.
func FormatNanoUSD(nanoUSD int64) string {
	if nanoUSD < 0 {
		return "?"
	}
	dollars := nanoUSD / nanoUSDPerUSD
	nanos := nanoUSD % nanoUSDPerUSD
	if nanos == 0 {
		return strconv.FormatInt(dollars, 10)
	}

	fraction := strconv.FormatInt(nanos, 10)
	for len(fraction) < 9 {
		fraction = "0" + fraction
	}
	for len(fraction) > 0 && fraction[len(fraction)-1] == '0' {
		fraction = fraction[:len(fraction)-1]
	}
	return strconv.FormatInt(dollars, 10) + "." + fraction
}
