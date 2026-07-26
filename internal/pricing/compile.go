package pricing

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Compile validates rules and creates an immutable lookup table.
func Compile(rules []Rule) (*Table, error) {
	table := &Table{
		userExact:    make(map[string]Rule),
		builtinExact: make(map[string]Rule),
	}
	for _, rule := range rules {
		if err := validateRule(rule); err != nil {
			return nil, err
		}
		if strings.HasSuffix(rule.Pattern, "*") {
			if rule.Source == SourceUser {
				table.userPrefixes = append(table.userPrefixes, rule)
			} else {
				table.builtinPrefixes = append(table.builtinPrefixes, rule)
			}
			continue
		}
		if rule.Source == SourceUser {
			if _, exists := table.userExact[rule.Pattern]; exists {
				return nil, fmt.Errorf("duplicate user pricing pattern %q", rule.Pattern)
			}
			table.userExact[rule.Pattern] = rule
			continue
		}
		if _, exists := table.builtinExact[rule.Pattern]; exists {
			return nil, fmt.Errorf("duplicate builtin pricing pattern %q", rule.Pattern)
		}
		table.builtinExact[rule.Pattern] = rule
	}
	sortPrefixes(table.userPrefixes)
	sortPrefixes(table.builtinPrefixes)
	if err := rejectDuplicatePrefixes(table.userPrefixes, SourceUser); err != nil {
		return nil, err
	}
	if err := rejectDuplicatePrefixes(table.builtinPrefixes, SourceBuiltin); err != nil {
		return nil, err
	}
	return table, nil
}

func validateRule(rule Rule) error {
	if err := ValidatePattern(rule.Pattern); err != nil {
		return err
	}
	if rule.Source != SourceBuiltin && rule.Source != SourceUser {
		return fmt.Errorf("unsupported pricing source %q", rule.Source)
	}

	priceSet := false
	for _, price := range [...]Price{
		rule.Prices.UncachedInput,
		rule.Prices.CacheRead,
		rule.Prices.CacheWrite5M,
		rule.Prices.CacheWrite1H,
		rule.Prices.Output,
	} {
		if math.IsNaN(price.Value) || math.IsInf(price.Value, 0) || price.Value < 0 {
			return fmt.Errorf("pricing price must be finite and non-negative")
		}
		priceSet = priceSet || price.Set
	}
	if !priceSet {
		return fmt.Errorf("pricing rule must set at least one price")
	}
	if rule.Source == SourceBuiltin && (rule.SourceURL == "" || rule.UpdatedAt.IsZero()) {
		return fmt.Errorf("builtin pricing rule requires source URL and update time")
	}
	if rule.Source == SourceUser && rule.SourceURL != "" {
		return fmt.Errorf("user pricing rule must not have a source URL")
	}
	return nil
}

// ValidatePattern validates a model-pricing exact or trailing-star prefix pattern.
func ValidatePattern(pattern string) error {
	if len(pattern) == 0 || len(pattern) > 255 {
		return fmt.Errorf("pricing pattern must be 1 through 255 bytes")
	}
	if strings.TrimSpace(pattern) != pattern {
		return fmt.Errorf("pricing pattern must not have surrounding whitespace")
	}
	for _, character := range pattern {
		if unicode.IsControl(character) {
			return fmt.Errorf("pricing pattern must not contain control characters")
		}
	}
	starCount := strings.Count(pattern, "*")
	if starCount > 1 || (starCount == 1 && !strings.HasSuffix(pattern, "*")) {
		return fmt.Errorf("pricing pattern may contain one trailing star")
	}
	if strings.Contains(pattern, "?") {
		return fmt.Errorf("pricing pattern must not contain question marks")
	}
	return nil
}

func sortPrefixes(rules []Rule) {
	sort.Slice(rules, func(left, right int) bool {
		leftPrefix := strings.TrimSuffix(rules[left].Pattern, "*")
		rightPrefix := strings.TrimSuffix(rules[right].Pattern, "*")
		if len(leftPrefix) != len(rightPrefix) {
			return len(leftPrefix) > len(rightPrefix)
		}
		return rules[left].Pattern < rules[right].Pattern
	})
}

func rejectDuplicatePrefixes(rules []Rule, source Source) error {
	for index := 1; index < len(rules); index++ {
		if rules[index-1].Pattern == rules[index].Pattern {
			return fmt.Errorf("duplicate %s pricing pattern %q", source, rules[index].Pattern)
		}
	}
	return nil
}

// Match finds the highest-priority rule for a non-empty upstream model.
func (table *Table) Match(upstreamModel string) (Rule, bool) {
	if table == nil || upstreamModel == "" {
		return Rule{}, false
	}
	if rule, ok := table.userExact[upstreamModel]; ok {
		return rule, true
	}
	if rule, ok := matchPrefix(table.userPrefixes, upstreamModel); ok {
		return rule, true
	}
	if rule, ok := table.builtinExact[upstreamModel]; ok {
		return rule, true
	}
	return matchPrefix(table.builtinPrefixes, upstreamModel)
}

func matchPrefix(rules []Rule, upstreamModel string) (Rule, bool) {
	for _, rule := range rules {
		if strings.HasPrefix(upstreamModel, strings.TrimSuffix(rule.Pattern, "*")) {
			return rule, true
		}
	}
	return Rule{}, false
}
