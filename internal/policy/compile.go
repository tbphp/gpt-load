package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/pricing"
)

// Empty 返回不可变的空配置
func Empty() *CompiledConfig {
	return &CompiledConfig{rules: nil}
}

// Clone 返回配置的深拷贝，对 nil 接收者安全并返回不可变空配置
func (c *CompiledConfig) Clone() *CompiledConfig {
	if c == nil {
		return Empty()
	}
	cloned := &CompiledConfig{groupPolicy: c.GroupPolicyMode()}
	if c.rules == nil {
		return cloned
	}
	cloned.rules = make([]Rule, len(c.rules))
	for i, r := range c.rules {
		cloned.rules[i] = r
		cloned.rules[i].When = cloneConditionNode(r.When)
	}
	return cloned
}

// Rules 返回配置中规则列表的深拷贝切片，保护内部 When 语法树指针
func (c *CompiledConfig) Rules() []Rule {
	if c == nil || len(c.rules) == 0 {
		return nil
	}
	res := make([]Rule, len(c.rules))
	for i, r := range c.rules {
		res[i] = r
		res[i].When = cloneConditionNode(r.When)
	}
	return res
}

func cloneConditionNode(node *ConditionNode) *ConditionNode {
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.InValues = slices.Clone(node.InValues)
	if node.Selector != nil {
		selector := *node.Selector
		cloned.Selector = &selector
	}
	cloned.Weekdays = slices.Clone(node.Weekdays)
	cloned.Ranges = slices.Clone(node.Ranges)
	cloned.Children = slices.Clone(node.Children)
	for i, child := range node.Children {
		cloned.Children[i] = cloneConditionNode(child)
	}
	cloned.Child = cloneConditionNode(node.Child)
	return &cloned
}

// Compile 使用默认注册表编译规则配置正文
func Compile(data []byte) (*CompiledConfig, error) {
	return CompileWithRegistry(data, DefaultRegistry)
}

func joinPath(base, elem string) string {
	if base == "" {
		return elem
	}
	return base + "." + elem
}

// CompileWithRegistry 使用指定的参数/谓词/动作注册表编译规则配置正文
func CompileWithRegistry(data []byte, reg *Registry) (*CompiledConfig, error) {
	if reg == nil {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidValue,
			Message: "registry cannot be nil",
		}
	}
	if data == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidValue,
			Message: "empty JSON input",
		}
	}
	if len(data) > MaxConfigBytes {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("config size %d bytes exceeds maximum allowed %d", len(data), MaxConfigBytes),
		}
	}
	if !utf8.Valid(data) {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidUTF8,
			Message: "input is not valid UTF-8",
		}
	}

	rawParsed, err := parseStrictJSON(data)
	if err != nil {
		return nil, err
	}

	rootObj, ok := rawParsed.(map[string]any)
	if !ok {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidType,
			Message: "root JSON value must be an object",
		}
	}

	// 根字段校验：必须且仅允许 schema_version、rules 与可选的 group_policy
	for k := range rootObj {
		if k != "schema_version" && k != "rules" && k != "group_policy" {
			return nil, &ValidationError{
				Path:    k,
				Code:    ErrCodeUnknownField,
				Message: fmt.Sprintf("unknown root field %q", k),
			}
		}
	}

	rawVer, hasVer := rootObj["schema_version"]
	if !hasVer {
		return nil, &ValidationError{
			Path:    "schema_version",
			Code:    ErrCodeMissingField,
			Message: "missing schema_version",
		}
	}

	// 严格检查 schema_version 为整型 1
	var verInt int64
	switch v := rawVer.(type) {
	case json.Number:
		var nErr error
		verInt, nErr = v.Int64()
		if nErr != nil {
			return nil, &ValidationError{
				Path:    "schema_version",
				Code:    ErrCodeInvalidValue,
				Message: "schema_version must be integer 1",
			}
		}
	default:
		return nil, &ValidationError{
			Path:    "schema_version",
			Code:    ErrCodeInvalidType,
			Message: "schema_version must be integer 1",
		}
	}
	if verInt != 1 {
		return nil, &ValidationError{
			Path:    "schema_version",
			Code:    ErrCodeInvalidValue,
			Message: fmt.Sprintf("unsupported schema_version %d, only version 1 is supported", verInt),
		}
	}

	// 可选 group_policy：缺省为 inherit；仅接受字符串 "inherit"/"override"，拒绝 null/未知值
	groupPolicy := GroupPolicyInherit
	if rawGroupPolicy, hasGroupPolicy := rootObj["group_policy"]; hasGroupPolicy {
		groupPolicyStr, ok := rawGroupPolicy.(string)
		if !ok {
			return nil, &ValidationError{
				Path:    "group_policy",
				Code:    ErrCodeInvalidType,
				Message: "group_policy must be string 'inherit' or 'override'",
			}
		}
		switch GroupPolicyMode(groupPolicyStr) {
		case GroupPolicyInherit, GroupPolicyOverride:
			groupPolicy = GroupPolicyMode(groupPolicyStr)
		default:
			return nil, &ValidationError{
				Path:    "group_policy",
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("invalid group_policy %q, must be 'inherit' or 'override'", groupPolicyStr),
			}
		}
	}

	rawRulesVal, hasRules := rootObj["rules"]
	if !hasRules {
		return nil, &ValidationError{
			Path:    "rules",
			Code:    ErrCodeMissingField,
			Message: "missing rules",
		}
	}
	rawRules, ok := rawRulesVal.([]any)
	if !ok {
		return nil, &ValidationError{
			Path:    "rules",
			Code:    ErrCodeInvalidType,
			Message: "rules must be an array",
		}
	}

	if len(rawRules) > MaxRulesPerConfig {
		return nil, &ValidationError{
			Path:    "rules",
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("rule count %d exceeds maximum allowed %d", len(rawRules), MaxRulesPerConfig),
		}
	}

	if len(rawRules) == 0 {
		return &CompiledConfig{groupPolicy: groupPolicy}, nil
	}

	seenIDs := make(map[string]struct{}, len(rawRules))
	compiledRules := make([]Rule, 0, len(rawRules))
	totalConfigNodes := 0

	for i, rawRule := range rawRules {
		rulePath := fmt.Sprintf("rules[%d]", i)
		ruleObj, ok := rawRule.(map[string]any)
		if !ok {
			return nil, &ValidationError{
				Path:    rulePath,
				Code:    ErrCodeInvalidType,
				Message: "rule must be an object",
			}
		}

		rule, nodeCount, err := compileRule(ruleObj, rulePath, reg)
		if err != nil {
			return nil, err
		}

		if _, exists := seenIDs[rule.ID]; exists {
			return nil, &ValidationError{
				Path:    rulePath + ".id",
				Code:    ErrCodeDuplicateKey,
				Message: fmt.Sprintf("duplicate rule id %q within config", rule.ID),
			}
		}
		seenIDs[rule.ID] = struct{}{}

		totalConfigNodes += nodeCount
		if totalConfigNodes > MaxNodesPerConfig {
			return nil, &ValidationError{
				Path:    rulePath,
				Code:    ErrCodeBudgetExceeded,
				Message: fmt.Sprintf("total condition nodes across config exceeds limit %d", MaxNodesPerConfig),
			}
		}

		compiledRules = append(compiledRules, rule)
	}

	return &CompiledConfig{rules: compiledRules, groupPolicy: groupPolicy}, nil
}

func compileRule(obj map[string]any, path string, reg *Registry) (Rule, int, error) {
	allowedFields := map[string]struct{}{
		"id":      {},
		"name":    {},
		"domain":  {},
		"enabled": {},
		"when":    {},
		"then":    {},
	}
	for k := range obj {
		if _, ok := allowedFields[k]; !ok {
			return Rule{}, 0, &ValidationError{
				Path:    joinPath(path, k),
				Code:    ErrCodeUnknownField,
				Message: fmt.Sprintf("unknown field %q in rule", k),
			}
		}
	}

	// id
	idVal, ok := obj["id"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".id",
			Code:    ErrCodeMissingField,
			Message: "missing rule id",
		}
	}
	idStr, ok := idVal.(string)
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".id",
			Code:    ErrCodeInvalidType,
			Message: "rule id must be string",
		}
	}
	if len(idStr) == 0 {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".id",
			Code:    ErrCodeInvalidValue,
			Message: "rule id cannot be empty",
		}
	}
	if len(idStr) > MaxIDLength {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".id",
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("rule id length %d exceeds maximum %d", len(idStr), MaxIDLength),
		}
	}
	if !idRegex.MatchString(idStr) {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".id",
			Code:    ErrCodeInvalidValue,
			Message: fmt.Sprintf("rule id %q must match ^[A-Za-z0-9_-]+$", idStr),
		}
	}

	// name
	nameVal, ok := obj["name"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".name",
			Code:    ErrCodeMissingField,
			Message: "missing rule name",
		}
	}
	nameStr, ok := nameVal.(string)
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".name",
			Code:    ErrCodeInvalidType,
			Message: "rule name must be string",
		}
	}
	if len(nameStr) == 0 {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".name",
			Code:    ErrCodeInvalidValue,
			Message: "rule name cannot be empty",
		}
	}
	if strings.TrimSpace(nameStr) != nameStr {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".name",
			Code:    ErrCodeInvalidValue,
			Message: "rule name cannot have leading or trailing whitespace",
		}
	}
	if utf8.RuneCountInString(nameStr) > MaxNameLength {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".name",
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("rule name character count %d exceeds maximum %d", utf8.RuneCountInString(nameStr), MaxNameLength),
		}
	}

	// domain
	domainVal, ok := obj["domain"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".domain",
			Code:    ErrCodeMissingField,
			Message: "missing rule domain",
		}
	}
	domainStr, ok := domainVal.(string)
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".domain",
			Code:    ErrCodeInvalidType,
			Message: "rule domain must be string",
		}
	}
	domain := Domain(domainStr)
	if !domain.Valid() {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".domain",
			Code:    ErrCodeInvalidValue,
			Message: fmt.Sprintf("invalid rule domain %q", domainStr),
		}
	}

	// enabled
	enabledVal, ok := obj["enabled"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".enabled",
			Code:    ErrCodeMissingField,
			Message: "missing rule enabled",
		}
	}
	enabled, ok := enabledVal.(bool)
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".enabled",
			Code:    ErrCodeInvalidType,
			Message: "rule enabled must be boolean",
		}
	}

	// then (先编译 then 便于关联 domain)
	thenVal, ok := obj["then"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".then",
			Code:    ErrCodeMissingField,
			Message: "missing rule then",
		}
	}
	action, err := compileAction(thenVal, domain, path+".then", reg)
	if err != nil {
		return Rule{}, 0, err
	}

	// when
	whenVal, ok := obj["when"]
	if !ok {
		return Rule{}, 0, &ValidationError{
			Path:    path + ".when",
			Code:    ErrCodeMissingField,
			Message: "missing rule when",
		}
	}

	nodeCount := 0
	conditionTree, err := compileCondition(whenVal, 1, path+".when", reg, &nodeCount)
	if err != nil {
		return Rule{}, 0, err
	}

	return Rule{
		ID:      idStr,
		Name:    nameStr,
		Domain:  domain,
		Enabled: enabled,
		When:    conditionTree,
		Then:    action,
	}, nodeCount, nil
}

func compileAction(raw any, domain Domain, path string, reg *Registry) (Action, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return Action{}, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidType,
			Message: "action must be an object",
		}
	}

	rawType, ok := obj["type"]
	if !ok {
		return Action{}, &ValidationError{
			Path:    path + ".type",
			Code:    ErrCodeMissingField,
			Message: "missing action type",
		}
	}
	typeStr, ok := rawType.(string)
	if !ok {
		return Action{}, &ValidationError{
			Path:    path + ".type",
			Code:    ErrCodeInvalidType,
			Message: "action type must be string",
		}
	}
	actionType := ActionType(typeStr)

	actionDesc, found := reg.FindAction(actionType)
	if !found {
		return Action{}, &ValidationError{
			Path:    path + ".type",
			Code:    ErrCodeUnknownField,
			Message: fmt.Sprintf("unregistered action type %q", typeStr),
		}
	}

	if actionDesc.Domain != domain {
		return Action{}, &ValidationError{
			Path:    path + ".type",
			Code:    ErrCodeDomainMismatch,
			Message: fmt.Sprintf("action %q belongs to domain %q, but rule domain is %q", actionType, actionDesc.Domain, domain),
		}
	}

	// 检查未知字段
	allowedFields := make(map[string]struct{}, len(actionDesc.Fields))
	for _, f := range actionDesc.Fields {
		allowedFields[f] = struct{}{}
	}
	for k := range obj {
		if _, ok := allowedFields[k]; !ok {
			return Action{}, &ValidationError{
				Path:    joinPath(path, k),
				Code:    ErrCodeUnknownField,
				Message: fmt.Sprintf("unknown field %q in action", k),
			}
		}
	}

	switch actionType {
	case ActionExcludeCandidate:
		return Action{Type: ActionExcludeCandidate}, nil

	case ActionMultiplyPrice:
		rawFactor, hasFactor := obj["factor"]
		if !hasFactor {
			return Action{}, &ValidationError{
				Path:    path + ".factor",
				Code:    ErrCodeMissingField,
				Message: "action multiply_price requires factor field",
			}
		}
		factorStr, ok := rawFactor.(string)
		if !ok {
			return Action{}, &ValidationError{
				Path:    path + ".factor",
				Code:    ErrCodeInvalidType,
				Message: "action multiply_price factor must be string",
			}
		}

		multiplier, err := pricing.ParsePriceMultiplier(factorStr)
		if err != nil {
			return Action{}, &ValidationError{
				Path:    path + ".factor",
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("invalid pricing multiplier %q: %v", factorStr, err),
			}
		}

		return Action{
			Type:       ActionMultiplyPrice,
			Factor:     factorStr,
			Multiplier: multiplier,
		}, nil

	default:
		return Action{}, &ValidationError{
			Path:    path + ".type",
			Code:    ErrCodeInvalidValue,
			Message: fmt.Sprintf("unsupported action type %q", actionType),
		}
	}
}

func compileCondition(raw any, depth int, path string, reg *Registry, totalNodes *int) (*ConditionNode, error) {
	if depth > MaxConditionDepth {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("condition depth %d exceeds maximum allowed %d", depth, MaxConditionDepth),
		}
	}

	*totalNodes++
	if *totalNodes > MaxNodesPerRule {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeBudgetExceeded,
			Message: fmt.Sprintf("condition node count exceeds maximum allowed %d per rule", MaxNodesPerRule),
		}
	}

	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidType,
			Message: "condition node must be an object",
		}
	}

	hasAll := obj["all"] != nil
	hasAny := obj["any"] != nil
	hasNot := obj["not"] != nil
	hasFact := obj["fact"] != nil
	hasPred := obj["predicate"] != nil

	formCount := 0
	if hasAll {
		formCount++
	}
	if hasAny {
		formCount++
	}
	if hasNot {
		formCount++
	}
	if hasFact {
		formCount++
	}
	if hasPred {
		formCount++
	}

	if formCount == 0 {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidCondition,
			Message: "empty condition node object, must specify one of all, any, not, fact, predicate",
		}
	}
	if formCount > 1 {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidCondition,
			Message: "condition node must specify exactly one of all, any, not, fact, predicate",
		}
	}

	if hasAll || hasAny {
		key := "all"
		kind := ConditionKindAll
		if hasAny {
			key = "any"
			kind = ConditionKindAny
		}
		for k := range obj {
			if k != key {
				return nil, &ValidationError{
					Path:    joinPath(path, k),
					Code:    ErrCodeUnknownField,
					Message: fmt.Sprintf("unknown field %q in '%s' condition node", k, key),
				}
			}
		}
		rawList, ok := obj[key].([]any)
		if !ok {
			return nil, &ValidationError{
				Path:    path + "." + key,
				Code:    ErrCodeInvalidType,
				Message: fmt.Sprintf("'%s' field must be an array", key),
			}
		}
		if len(rawList) == 0 {
			return nil, &ValidationError{
				Path:    path + "." + key,
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("'%s' array cannot be empty", key),
			}
		}
		if len(rawList) > MaxListItems {
			return nil, &ValidationError{
				Path:    path + "." + key,
				Code:    ErrCodeBudgetExceeded,
				Message: fmt.Sprintf("'%s' items count %d exceeds maximum %d", key, len(rawList), MaxListItems),
			}
		}

		children := make([]*ConditionNode, 0, len(rawList))
		for i, item := range rawList {
			childPath := fmt.Sprintf("%s.%s[%d]", path, key, i)
			childNode, err := compileCondition(item, depth+1, childPath, reg, totalNodes)
			if err != nil {
				return nil, err
			}
			children = append(children, childNode)
		}
		return &ConditionNode{Kind: kind, Children: children}, nil
	}

	if hasNot {
		for k := range obj {
			if k != "not" {
				return nil, &ValidationError{
					Path:    joinPath(path, k),
					Code:    ErrCodeUnknownField,
					Message: fmt.Sprintf("unknown field %q in 'not' condition node", k),
				}
			}
		}
		childPath := path + ".not"
		childNode, err := compileCondition(obj["not"], depth+1, childPath, reg, totalNodes)
		if err != nil {
			return nil, err
		}
		return &ConditionNode{Kind: ConditionKindNot, Child: childNode}, nil
	}

	if hasPred {
		predName, ok := obj["predicate"].(string)
		if !ok {
			return nil, &ValidationError{
				Path:    path + ".predicate",
				Code:    ErrCodeInvalidType,
				Message: "predicate name must be string",
			}
		}
		_, found := reg.FindPredicate(predName)
		if !found {
			return nil, &ValidationError{
				Path:    path + ".predicate",
				Code:    ErrCodeUnknownField,
				Message: fmt.Sprintf("unregistered predicate %q", predName),
			}
		}

		switch predName {
		case "time_window":
			allowed := map[string]struct{}{
				"predicate": {},
				"weekdays":  {},
				"ranges":    {},
			}
			for k := range obj {
				if _, ok := allowed[k]; !ok {
					return nil, &ValidationError{
						Path:    joinPath(path, k),
						Code:    ErrCodeUnknownField,
						Message: fmt.Sprintf("unknown field %q in time_window predicate", k),
					}
				}
			}

			rawWeekdays, hasWk := obj["weekdays"].([]any)
			if !hasWk || len(rawWeekdays) == 0 {
				return nil, &ValidationError{
					Path:    path + ".weekdays",
					Code:    ErrCodeInvalidValue,
					Message: "time_window requires non-empty weekdays array",
				}
			}
			if len(rawWeekdays) > 7 {
				return nil, &ValidationError{
					Path:    path + ".weekdays",
					Code:    ErrCodeBudgetExceeded,
					Message: "time_window weekdays array cannot have more than 7 elements",
				}
			}

			seenWk := make(map[int]struct{})
			weekdays := make([]int, 0, len(rawWeekdays))
			for _, w := range rawWeekdays {
				var wkInt int
				switch wv := w.(type) {
				case json.Number:
					parsed, pErr := strconv.Atoi(wv.String())
					if pErr != nil {
						return nil, &ValidationError{
							Path:    path + ".weekdays",
							Code:    ErrCodeInvalidType,
							Message: "weekday must be integer 0-6",
						}
					}
					wkInt = parsed
				default:
					return nil, &ValidationError{
						Path:    path + ".weekdays",
						Code:    ErrCodeInvalidType,
						Message: "weekday must be integer 0-6",
					}
				}
				if wkInt < 0 || wkInt > 6 {
					return nil, &ValidationError{
						Path:    path + ".weekdays",
						Code:    ErrCodeInvalidValue,
						Message: fmt.Sprintf("weekday %d out of range (0-6)", wkInt),
					}
				}
				if _, exists := seenWk[wkInt]; exists {
					return nil, &ValidationError{
						Path:    path + ".weekdays",
						Code:    ErrCodeDuplicateKey,
						Message: fmt.Sprintf("duplicate weekday %d", wkInt),
					}
				}
				seenWk[wkInt] = struct{}{}
				weekdays = append(weekdays, wkInt)
			}

			rawRanges, hasRanges := obj["ranges"].([]any)
			if !hasRanges || len(rawRanges) == 0 {
				return nil, &ValidationError{
					Path:    path + ".ranges",
					Code:    ErrCodeInvalidValue,
					Message: "time_window requires non-empty ranges array",
				}
			}
			if len(rawRanges) > MaxListItems {
				return nil, &ValidationError{
					Path:    path + ".ranges",
					Code:    ErrCodeBudgetExceeded,
					Message: fmt.Sprintf("ranges count %d exceeds maximum %d", len(rawRanges), MaxListItems),
				}
			}

			timeRanges := make([]TimeRange, 0, len(rawRanges))
			for rIdx, rItem := range rawRanges {
				rangePair, ok := rItem.([]any)
				if !ok || len(rangePair) != 2 {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.ranges[%d]", path, rIdx),
						Code:    ErrCodeInvalidValue,
						Message: "each time range must be an array of exactly 2 string elements [start, end]",
					}
				}
				startStr, ok1 := rangePair[0].(string)
				endStr, ok2 := rangePair[1].(string)
				if !ok1 || !ok2 {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.ranges[%d]", path, rIdx),
						Code:    ErrCodeInvalidType,
						Message: "start and end times must be string 'HH:MM'",
					}
				}
				tr, err := compileTimeRange(startStr, endStr)
				if err != nil {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.ranges[%d]", path, rIdx),
						Code:    ErrCodeInvalidValue,
						Message: err.Error(),
					}
				}
				timeRanges = append(timeRanges, tr)
			}

			return &ConditionNode{
				Kind:     ConditionKindTimeWindow,
				Weekdays: weekdays,
				Ranges:   timeRanges,
			}, nil

		default:
			return nil, &ValidationError{
				Path:    path + ".predicate",
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("unsupported predicate %q", predName),
			}
		}
	}

	// fact
	factKey, ok := obj["fact"].(string)
	if !ok {
		return nil, &ValidationError{
			Path:    path + ".fact",
			Code:    ErrCodeInvalidType,
			Message: "fact must be string",
		}
	}
	desc, found := reg.FindParam(factKey)
	if !found {
		return nil, &ValidationError{
			Path:    path + ".fact",
			Code:    ErrCodeUnknownField,
			Message: fmt.Sprintf("unknown parameter %q", factKey),
		}
	}

	rawOp, hasOp := obj["op"]
	if !hasOp {
		return nil, &ValidationError{
			Path:    path + ".op",
			Code:    ErrCodeMissingField,
			Message: "missing comparison operator 'op'",
		}
	}
	opStr, ok := rawOp.(string)
	if !ok {
		return nil, &ValidationError{
			Path:    path + ".op",
			Code:    ErrCodeInvalidType,
			Message: "comparison operator 'op' must be string",
		}
	}

	validOp := false
	for _, supportedOp := range desc.Operators {
		if opStr == supportedOp {
			validOp = true
			break
		}
	}
	if !validOp {
		return nil, &ValidationError{
			Path:    path + ".op",
			Code:    ErrCodeUnsupportedOperator,
			Message: fmt.Sprintf("operator %q is not supported for parameter %q", opStr, factKey),
		}
	}

	rawValue, hasValue := obj["value"]
	if !hasValue {
		return nil, &ValidationError{
			Path:    path + ".value",
			Code:    ErrCodeMissingField,
			Message: "missing comparison 'value'",
		}
	}

	isQuota := (desc.Key == "credential.quota.remaining_ratio")
	node := &ConditionNode{
		Kind:      ConditionKindParam,
		Fact:      factKey,
		ParamType: desc.Type,
		IsQuota:   isQuota,
		Op:        opStr,
	}

	allowedFactFields := map[string]struct{}{
		"fact":  {},
		"op":    {},
		"value": {},
	}
	if desc.SelectorConstraint != nil {
		allowedFactFields["select"] = struct{}{}
		allowedFactFields["reduce"] = struct{}{}
	}
	for k := range obj {
		if _, ok := allowedFactFields[k]; !ok {
			return nil, &ValidationError{
				Path:    joinPath(path, k),
				Code:    ErrCodeUnknownField,
				Message: fmt.Sprintf("unknown field %q in fact condition", k),
			}
		}
	}

	// 编译 value 及参数专属字段
	switch desc.Type {
	case ParamTypeString:
		if opStr == "eq" {
			valStr, ok := rawValue.(string)
			if !ok {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidType,
					Message: "value must be string for operator eq",
				}
			}
			if strings.TrimSpace(valStr) != valStr {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidValue,
					Message: "string value cannot have leading or trailing whitespace",
				}
			}
			if utf8.RuneCountInString(valStr) > MaxModelLength {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeBudgetExceeded,
					Message: fmt.Sprintf("string value length %d exceeds maximum %d", utf8.RuneCountInString(valStr), MaxModelLength),
				}
			}
			node.StrValue = valStr
		} else if opStr == "in" {
			rawArr, ok := rawValue.([]any)
			if !ok {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidType,
					Message: "value must be array for operator in",
				}
			}
			if len(rawArr) == 0 {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidValue,
					Message: "'in' value list cannot be empty",
				}
			}
			if len(rawArr) > MaxListItems {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeBudgetExceeded,
					Message: fmt.Sprintf("'in' items count %d exceeds maximum %d", len(rawArr), MaxListItems),
				}
			}

			seenItems := make(map[string]struct{}, len(rawArr))
			inValues := make([]string, 0, len(rawArr))
			for vIdx, item := range rawArr {
				itemStr, ok := item.(string)
				if !ok {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.value[%d]", path, vIdx),
						Code:    ErrCodeInvalidType,
						Message: "each item in 'in' value list must be string",
					}
				}
				if strings.TrimSpace(itemStr) != itemStr {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.value[%d]", path, vIdx),
						Code:    ErrCodeInvalidValue,
						Message: "item in 'in' value list cannot have leading or trailing whitespace",
					}
				}
				if utf8.RuneCountInString(itemStr) > MaxModelLength {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.value[%d]", path, vIdx),
						Code:    ErrCodeBudgetExceeded,
						Message: fmt.Sprintf("item length %d exceeds maximum %d", utf8.RuneCountInString(itemStr), MaxModelLength),
					}
				}
				if _, exists := seenItems[itemStr]; exists {
					return nil, &ValidationError{
						Path:    fmt.Sprintf("%s.value[%d]", path, vIdx),
						Code:    ErrCodeDuplicateKey,
						Message: fmt.Sprintf("duplicate item %q in 'in' value list", itemStr),
					}
				}
				seenItems[itemStr] = struct{}{}
				inValues = append(inValues, itemStr)
			}
			node.InValues = inValues
		}

	case ParamTypeNumber:
		var numVal float64
		var rawNumStr string
		switch nv := rawValue.(type) {
		case json.Number:
			rawNumStr = nv.String()
			parsed, pErr := nv.Float64()
			if pErr != nil {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidValue,
					Message: "numeric value is invalid",
				}
			}
			numVal = parsed
		default:
			return nil, &ValidationError{
				Path:    path + ".value",
				Code:    ErrCodeInvalidType,
				Message: "value must be number",
			}
		}

		// 使用 big.Rat 进行基于原始十进制的高精度范围校验
		ratVal, ok := new(big.Rat).SetString(rawNumStr)
		if !ok {
			return nil, &ValidationError{
				Path:    path + ".value",
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("invalid decimal number %q", rawNumStr),
			}
		}

		if isQuota {
			zero := big.NewRat(0, 1)
			one := big.NewRat(1, 1)
			if ratVal.Cmp(zero) < 0 || ratVal.Cmp(one) > 0 {
				return nil, &ValidationError{
					Path:    path + ".value",
					Code:    ErrCodeInvalidValue,
					Message: fmt.Sprintf("quota ratio %s out of range [0.0, 1.0]", rawNumStr),
				}
			}

			// 编译 select 与 reduce
			rawSel, hasSel := obj["select"]
			if !hasSel {
				return nil, &ValidationError{
					Path:    path + ".select",
					Code:    ErrCodeMissingField,
					Message: "quota parameter requires 'select' object",
				}
			}
			selObj, ok := rawSel.(map[string]any)
			if !ok {
				return nil, &ValidationError{
					Path:    path + ".select",
					Code:    ErrCodeInvalidType,
					Message: "'select' must be an object",
				}
			}
			allowedSel := map[string]struct{}{
				"scope":          {},
				"window_seconds": {},
			}
			for k := range selObj {
				if _, ok := allowedSel[k]; !ok {
					return nil, &ValidationError{
						Path:    joinPath(path+".select", k),
						Code:    ErrCodeUnknownField,
						Message: fmt.Sprintf("unknown field %q in select object", k),
					}
				}
			}

			scopeStr, ok := selObj["scope"].(string)
			if !ok || scopeStr != "account" {
				return nil, &ValidationError{
					Path:    path + ".select.scope",
					Code:    ErrCodeInvalidValue,
					Message: "quota select.scope must be 'account'",
				}
			}

			rawWin := selObj["window_seconds"]
			var winSec int
			switch wv := rawWin.(type) {
			case json.Number:
				parsed, pErr := strconv.Atoi(wv.String())
				if pErr != nil {
					return nil, &ValidationError{
						Path:    path + ".select.window_seconds",
						Code:    ErrCodeInvalidType,
						Message: "window_seconds must be integer",
					}
				}
				winSec = parsed
			default:
				return nil, &ValidationError{
					Path:    path + ".select.window_seconds",
					Code:    ErrCodeInvalidType,
					Message: "window_seconds must be integer",
				}
			}
			if winSec <= 0 {
				return nil, &ValidationError{
					Path:    path + ".select.window_seconds",
					Code:    ErrCodeInvalidValue,
					Message: fmt.Sprintf("window_seconds %d must be greater than 0", winSec),
				}
			}

			rawRed, hasRed := obj["reduce"]
			if !hasRed {
				return nil, &ValidationError{
					Path:    path + ".reduce",
					Code:    ErrCodeMissingField,
					Message: "quota parameter requires 'reduce' field",
				}
			}
			redStr, ok := rawRed.(string)
			if !ok || redStr != "min" {
				return nil, &ValidationError{
					Path:    path + ".reduce",
					Code:    ErrCodeInvalidValue,
					Message: "quota reducer must be 'min'",
				}
			}

			node.Selector = &QuotaSelector{Scope: scopeStr, WindowSeconds: winSec}
			node.Reduce = redStr
		}

		// 检查 float64 舍入是否改变了十进制边界语义：
		// 将规范化十进制字符串 FormatFloat(numVal, 'g', -1, 64) 解析为 big.Rat，
		// 再与原始十进制有理数 ratVal 比较，避免误将 float64 二进制表示当成十进制语义差异。
		var numShift int8
		normStr := strconv.FormatFloat(numVal, 'g', -1, 64)
		if normRat, normOk := new(big.Rat).SetString(normStr); normOk {
			cmp := ratVal.Cmp(normRat)
			if cmp < 0 {
				numShift = -1
			} else if cmp > 0 {
				numShift = 1
			}
		}
		node.NumValue = numVal
		node.NumShift = numShift
	}

	return node, nil
}

// parseStrictJSON 使用 json.Decoder 严格递归解析 JSON：
// 拒绝 duplicate object keys，拒绝 null，拒绝尾随 JSON token
func parseStrictJSON(data []byte) (any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidValue,
			Message: "empty JSON input",
		}
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()

	val, err := parseStrictValue(dec, 1, "")
	if err != nil {
		return nil, err
	}

	var extra json.Token
	extra, err = dec.Token()
	if !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, &ValidationError{
				Path:    "",
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("unexpected trailing JSON token %v", extra),
			}
		}
		return nil, &ValidationError{
			Path:    "",
			Code:    ErrCodeInvalidValue,
			Message: fmt.Sprintf("trailing JSON data error: %v", err),
		}
	}

	return val, nil
}

func parseStrictValue(dec *json.Decoder, depth int, path string) (any, error) {
	if depth > MaxJSONDepth {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeBudgetExceeded,
			Message: "JSON nesting depth exceeds maximum limit",
		}
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidValue,
			Message: err.Error(),
		}
	}
	if tok == nil {
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidValue,
			Message: "null value is not allowed",
		}
	}

	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := make(map[string]any)
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, &ValidationError{
						Path:    path,
						Code:    ErrCodeInvalidValue,
						Message: err.Error(),
					}
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, &ValidationError{
						Path:    path,
						Code:    ErrCodeInvalidType,
						Message: fmt.Sprintf("expected string object key, got %T", keyTok),
					}
				}
				subPath := joinPath(path, key)
				if _, exists := obj[key]; exists {
					return nil, &ValidationError{
						Path:    subPath,
						Code:    ErrCodeDuplicateKey,
						Message: fmt.Sprintf("duplicate field %q", key),
					}
				}

				val, err := parseStrictValue(dec, depth+1, subPath)
				if err != nil {
					return nil, err
				}
				obj[key] = val
			}
			endTok, err := dec.Token()
			if err != nil || endTok != json.Delim('}') {
				return nil, &ValidationError{
					Path:    path,
					Code:    ErrCodeInvalidValue,
					Message: "expected '}'",
				}
			}
			return obj, nil

		case '[':
			arr := make([]any, 0)
			idx := 0
			for dec.More() {
				subPath := fmt.Sprintf("%s[%d]", path, idx)
				val, err := parseStrictValue(dec, depth+1, subPath)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
				idx++
			}
			endTok, err := dec.Token()
			if err != nil || endTok != json.Delim(']') {
				return nil, &ValidationError{
					Path:    path,
					Code:    ErrCodeInvalidValue,
					Message: "expected ']'",
				}
			}
			return arr, nil

		default:
			return nil, &ValidationError{
				Path:    path,
				Code:    ErrCodeInvalidValue,
				Message: fmt.Sprintf("unexpected delimiter %v", t),
			}
		}

	case string:
		return t, nil
	case bool:
		return t, nil
	case json.Number:
		return t, nil
	default:
		return nil, &ValidationError{
			Path:    path,
			Code:    ErrCodeInvalidType,
			Message: fmt.Sprintf("unexpected JSON token type %T", tok),
		}
	}
}
