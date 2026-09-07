package control

import (
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
)

func normalizePriceMultiplier(field optionalField[string]) (pricing.PriceMultiplier, error) {
	if !field.Set {
		return pricing.DefaultPriceMultiplier, nil
	}
	if field.Null {
		return 0, app_errors.ErrValidation
	}
	value, err := pricing.ParsePriceMultiplier(field.Value)
	if err != nil {
		return 0, app_errors.ErrValidation
	}
	return value, nil
}

func priceMultiplierStorage(value pricing.PriceMultiplier) *int64 {
	micros := int64(value)
	return &micros
}

func priceMultiplierFromStorage(value *int64) pricing.PriceMultiplier {
	if value == nil {
		return pricing.DefaultPriceMultiplier
	}
	return pricing.PriceMultiplier(*value)
}

func priceMultiplierResponse(value *int64) string {
	return pricing.FormatPriceMultiplier(priceMultiplierFromStorage(value))
}

func priceMultiplierDigest(value pricing.PriceMultiplier) string {
	if value == pricing.DefaultPriceMultiplier {
		return ""
	}
	return pricing.FormatPriceMultiplier(value)
}
