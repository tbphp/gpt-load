package contentcoding

import "strings"

func IdentityAcceptable(values []string) bool {
	explicitQuality, explicitFound := -1, false
	wildcardQuality, wildcardFound := -1, false

	for _, value := range values {
		for _, rawItem := range strings.Split(value, ",") {
			token, quality, ok := parseAcceptEncodingItem(rawItem)
			if !ok {
				continue
			}
			switch token {
			case string(Identity):
				explicitFound = true
				explicitQuality = max(explicitQuality, quality)
			case "*":
				wildcardFound = true
				wildcardQuality = max(wildcardQuality, quality)
			}
		}
	}

	if explicitFound {
		return explicitQuality > 0
	}
	if wildcardFound {
		return wildcardQuality > 0
	}
	return true
}

func parseAcceptEncodingItem(raw string) (string, int, bool) {
	parts := strings.Split(raw, ";")
	token := strings.ToLower(strings.TrimSpace(parts[0]))
	if token == "" {
		return "", 0, false
	}
	if len(parts) == 1 {
		return token, 1000, true
	}
	if len(parts) != 2 {
		return "", 0, false
	}

	parameter := strings.SplitN(parts[1], "=", 2)
	if len(parameter) != 2 || !strings.EqualFold(strings.TrimSpace(parameter[0]), "q") {
		return "", 0, false
	}
	quality, ok := parseQvalue(strings.TrimSpace(parameter[1]))
	if !ok {
		return "", 0, false
	}
	return token, quality, true
}

func parseQvalue(value string) (int, bool) {
	if value == "0" {
		return 0, true
	}
	if value == "1" {
		return 1000, true
	}
	if len(value) < 2 || value[1] != '.' {
		return 0, false
	}

	fraction := value[2:]
	if len(fraction) > 3 {
		return 0, false
	}
	quality := 0
	for _, digit := range fraction {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		quality = quality*10 + int(digit-'0')
	}
	for digits := len(fraction); digits < 3; digits++ {
		quality *= 10
	}

	switch value[0] {
	case '0':
		return quality, true
	case '1':
		return 1000, quality == 0
	default:
		return 0, false
	}
}
