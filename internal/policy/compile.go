package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/pricing"
)

// validationError 构造带字段路径与可读原因的编译校验错误。
func validationError(path, message string) error {
	return &ValidationError{Path: path, Message: message}
}

// Empty 返回不可变的空配置
func Empty() *CompiledConfig {
	return &CompiledConfig{rules: nil}
}

// Rules 返回配置中编译后的规则列表切片（只读）
func (c *CompiledConfig) Rules() []Rule {
	if c == nil {
		return nil
	}
	return c.rules
}

// Compile 编译规则配置正文

func joinPath(base, elem string) string {
	if base == "" {
		return elem
	}
	return base + "." + elem
}

func Compile(data []byte) (*CompiledConfig, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, validationError("", "empty JSON input")
	}
	if len(data) > MaxConfigBytes {
		return nil, validationError("", fmt.Sprintf("config size %d bytes exceeds maximum allowed %d", len(data), MaxConfigBytes))
	}
	if !utf8.Valid(data) {
		return nil, validationError("", "input is not valid UTF-8")
	}

	var rootObj map[string]any
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&rootObj); err != nil {
		return nil, validationError("", err.Error())
	}
	if dec.More() {
		return nil, validationError("", "unexpected trailing JSON data")
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, validationError("", "unexpected trailing JSON data")
	}
	if rootObj == nil {
		return nil, validationError("", "root JSON value must be an object")
	}

	// 扩展字段静默忽略，保持向前兼容。
	rawVer, hasVer := rootObj["schema_version"]
	if !hasVer {
		return nil, validationError("schema_version", "missing schema_version")
	}

	// 严格检查 schema_version 为整型 1
	var verInt int64
	switch v := rawVer.(type) {
	case json.Number:
		var nErr error
		verInt, nErr = v.Int64()
		if nErr != nil {
			return nil, validationError("schema_version", "schema_version must be integer 1")
		}
	default:
		return nil, validationError("schema_version", "schema_version must be integer 1")
	}
	if verInt != 1 {
		return nil, validationError("schema_version", fmt.Sprintf("unsupported schema_version %d, only version 1 is supported", verInt))
	}

	// 可选 group_policy：缺省为 inherit；仅接受字符串 "inherit"/"override"，拒绝 null/未知值
	groupPolicy := GroupPolicyInherit
	if rawGroupPolicy, hasGroupPolicy := rootObj["group_policy"]; hasGroupPolicy {
		groupPolicyStr, ok := rawGroupPolicy.(string)
		if !ok {
			return nil, validationError("group_policy", "group_policy must be string 'inherit' or 'override'")
		}
		switch GroupPolicyMode(groupPolicyStr) {
		case GroupPolicyInherit, GroupPolicyOverride:
			groupPolicy = GroupPolicyMode(groupPolicyStr)
		default:
			return nil, validationError("group_policy", fmt.Sprintf("invalid group_policy %q, must be 'inherit' or 'override'", groupPolicyStr))
		}
	}

	rawRulesVal, hasRules := rootObj["rules"]
	if !hasRules {
		return nil, validationError("rules", "missing rules")
	}
	rawRules, ok := rawRulesVal.([]any)
	if !ok {
		return nil, validationError("rules", "rules must be an array")
	}

	if len(rawRules) > MaxRulesPerConfig {
		return nil, validationError("rules", fmt.Sprintf("rule count %d exceeds maximum allowed %d", len(rawRules), MaxRulesPerConfig))
	}

	if len(rawRules) == 0 {
		return &CompiledConfig{groupPolicy: groupPolicy, nodeCount: 0}, nil
	}

	seenIDs := make(map[string]struct{}, len(rawRules))
	compiledRules := make([]Rule, 0, len(rawRules))
	totalConfigNodes := 0

	for i, rawRule := range rawRules {
		rulePath := fmt.Sprintf("rules[%d]", i)
		ruleObj, ok := rawRule.(map[string]any)
		if !ok {
			return nil, validationError(rulePath, "rule must be an object")
		}

		rule, nodeCount, err := compileRule(ruleObj, rulePath)
		if err != nil {
			return nil, err
		}

		if _, exists := seenIDs[rule.ID]; exists {
			return nil, validationError(rulePath+".id", fmt.Sprintf("duplicate rule id %q within config", rule.ID))
		}
		seenIDs[rule.ID] = struct{}{}

		totalConfigNodes += nodeCount
		if totalConfigNodes > MaxNodesPerConfig {
			return nil, validationError(rulePath, fmt.Sprintf("total condition nodes across config exceeds limit %d", MaxNodesPerConfig))
		}

		compiledRules = append(compiledRules, rule)
	}

	return &CompiledConfig{rules: compiledRules, groupPolicy: groupPolicy, nodeCount: totalConfigNodes}, nil
}

func compileRule(obj map[string]any, path string) (Rule, int, error) {
	// id
	idVal, ok := obj["id"]
	if !ok {
		return Rule{}, 0, validationError(path+".id", "missing rule id")
	}
	idStr, ok := idVal.(string)
	if !ok {
		return Rule{}, 0, validationError(path+".id", "rule id must be string")
	}
	if len(idStr) == 0 {
		return Rule{}, 0, validationError(path+".id", "rule id cannot be empty")
	}
	if len(idStr) > MaxIDLength {
		return Rule{}, 0, validationError(path+".id", fmt.Sprintf("rule id length %d exceeds maximum %d", len(idStr), MaxIDLength))
	}
	if !idRegex.MatchString(idStr) {
		return Rule{}, 0, validationError(path+".id", fmt.Sprintf("rule id %q must match ^[A-Za-z0-9_-]+$", idStr))
	}

	// name
	nameVal, ok := obj["name"]
	if !ok {
		return Rule{}, 0, validationError(path+".name", "missing rule name")
	}
	nameStr, ok := nameVal.(string)
	if !ok {
		return Rule{}, 0, validationError(path+".name", "rule name must be string")
	}
	if len(nameStr) == 0 {
		return Rule{}, 0, validationError(path+".name", "rule name cannot be empty")
	}
	if strings.TrimSpace(nameStr) != nameStr {
		return Rule{}, 0, validationError(path+".name", "rule name cannot have leading or trailing whitespace")
	}
	if utf8.RuneCountInString(nameStr) > MaxNameLength {
		return Rule{}, 0, validationError(path+".name", fmt.Sprintf("rule name character count %d exceeds maximum %d", utf8.RuneCountInString(nameStr), MaxNameLength))
	}

	// domain (未传或为空默认 scheduling，若显式传入则校验合法枚举)
	var domain Domain = DomainScheduling
	if domainVal, ok := obj["domain"]; ok && domainVal != nil {
		domainStr, isStr := domainVal.(string)
		if !isStr {
			return Rule{}, 0, validationError(path+".domain", "rule domain must be string")
		}
		if domainStr != "" {
			domain = Domain(domainStr)
			if !domain.Valid() {
				return Rule{}, 0, validationError(path+".domain", fmt.Sprintf("invalid rule domain %q", domainStr))
			}
		}
	}

	// enabled
	enabledVal, ok := obj["enabled"]
	if !ok {
		return Rule{}, 0, validationError(path+".enabled", "missing rule enabled")
	}
	enabled, ok := enabledVal.(bool)
	if !ok {
		return Rule{}, 0, validationError(path+".enabled", "rule enabled must be boolean")
	}

	// actions (必须为非空 Action 数组，上限 16 项)
	actionsVal, ok := obj["actions"]
	if !ok {
		return Rule{}, 0, validationError(path+".actions", "missing rule actions")
	}
	actionsArr, ok := actionsVal.([]any)
	if !ok {
		return Rule{}, 0, validationError(path+".actions", "rule actions must be array")
	}
	if len(actionsArr) == 0 {
		return Rule{}, 0, validationError(path+".actions", "rule actions cannot be empty")
	}
	if len(actionsArr) > MaxActionsPerRule {
		return Rule{}, 0, validationError(path+".actions", fmt.Sprintf("rule actions count %d exceeds maximum %d", len(actionsArr), MaxActionsPerRule))
	}
	actions := make([]Action, 0, len(actionsArr))
	for idx, actRaw := range actionsArr {
		act, err := compileAction(actRaw, fmt.Sprintf("%s.actions[%d]", path, idx))
		if err != nil {
			return Rule{}, 0, err
		}
		actions = append(actions, act)
	}

	// when
	whenVal, ok := obj["when"]
	if !ok {
		return Rule{}, 0, validationError(path+".when", "missing rule when")
	}

	nodeCount := 0
	conditionTree, err := compileCondition(whenVal, 1, path+".when", &nodeCount)
	if err != nil {
		return Rule{}, 0, err
	}

	return Rule{
		ID:      idStr,
		Name:    nameStr,
		Domain:  domain,
		Enabled: enabled,
		When:    conditionTree,
		Actions: actions,
	}, nodeCount, nil
}

// compileStringItems 校验并收集字符串列表元素：类型、首尾空白、长度与重复。
// fieldPath 是列表自身的字段路径；allowEmpty 决定空串是否合法；
// itemNoun 与 subject 只用于拼接与各调用点原有文案一致的消息，不改变消息主语。
func compileStringItems(rawArr []any, fieldPath, itemNoun, subject string, allowEmpty bool) ([]string, error) {
	seen := make(map[string]struct{}, len(rawArr))
	values := make([]string, 0, len(rawArr))
	for idx, item := range rawArr {
		itemStr, ok := item.(string)
		if !ok {
			return nil, validationError(fmt.Sprintf("%s[%d]", fieldPath, idx), fmt.Sprintf("each item in %s must be string", subject))
		}
		if strings.TrimSpace(itemStr) != itemStr {
			return nil, validationError(fmt.Sprintf("%s[%d]", fieldPath, idx), fmt.Sprintf("item in %s cannot have leading or trailing whitespace", subject))
		}
		if !allowEmpty && itemStr == "" {
			return nil, validationError(fmt.Sprintf("%s[%d]", fieldPath, idx), fmt.Sprintf("item in %s cannot be empty", subject))
		}
		if length := utf8.RuneCountInString(itemStr); length > MaxModelLength {
			return nil, validationError(fmt.Sprintf("%s[%d]", fieldPath, idx), fmt.Sprintf("%s length %d exceeds maximum %d", itemNoun, length, MaxModelLength))
		}
		if _, exists := seen[itemStr]; exists {
			return nil, validationError(fmt.Sprintf("%s[%d]", fieldPath, idx), fmt.Sprintf("duplicate %s %q in %s", itemNoun, itemStr, subject))
		}
		seen[itemStr] = struct{}{}
		values = append(values, itemStr)
	}
	return values, nil
}

func compileAction(raw any, path string) (Action, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return Action{}, validationError(path, "action must be an object")
	}

	rawType, ok := obj["type"]
	if !ok {
		return Action{}, validationError(path+".type", "missing action type")
	}
	typeStr, ok := rawType.(string)
	if !ok {
		return Action{}, validationError(path+".type", "action type must be string")
	}
	actionType := ActionType(typeStr)

	if _, found := FindAction(actionType); !found {
		return Action{}, validationError(path+".type", fmt.Sprintf("unregistered action type %q", typeStr))
	}

	// 动作按自身类型自描述（如 exclude_candidate/exclude_models 作用于调度，multiply_price 作用于计费），
	// 规则本身不再对动作所属 domain 实施互斥拦截

	switch actionType {
	case ActionExcludeCandidate:
		return Action{Type: ActionExcludeCandidate}, nil

	case ActionExcludeModels:
		rawModels, hasModels := obj["models"]
		if !hasModels {
			return Action{}, validationError(path+".models", "action exclude_models requires models field")
		}
		rawArr, ok := rawModels.([]any)
		if !ok {
			return Action{}, validationError(path+".models", "action exclude_models models must be array")
		}
		if len(rawArr) == 0 {
			return Action{}, validationError(path+".models", "action exclude_models models array cannot be empty")
		}
		if len(rawArr) > MaxListItems {
			return Action{}, validationError(path+".models", fmt.Sprintf("action exclude_models models count %d exceeds maximum %d", len(rawArr), MaxListItems))
		}
		models, err := compileStringItems(rawArr, path+".models", "model", "exclude_models models", false)
		if err != nil {
			return Action{}, err
		}
		slices.Sort(models)
		return Action{Type: ActionExcludeModels, Models: models}, nil

	case ActionMultiplyPrice:
		rawFactor, hasFactor := obj["factor"]
		if !hasFactor {
			return Action{}, validationError(path+".factor", "action multiply_price requires factor field")
		}
		factorStr, ok := rawFactor.(string)
		if !ok {
			return Action{}, validationError(path+".factor", "action multiply_price factor must be string")
		}

		// 倍率解析遵循 pricing.PriceMultiplier 规范 (0~1000, 最多 6 位小数)。
		multiplier, err := pricing.ParsePriceMultiplier(factorStr)
		if err != nil {
			return Action{}, validationError(path+".factor", fmt.Sprintf("invalid pricing multiplier %q: %v", factorStr, err))
		}

		return Action{
			Type:       ActionMultiplyPrice,
			Factor:     factorStr,
			Multiplier: multiplier,
		}, nil

	default:
		return Action{}, validationError(path+".type", fmt.Sprintf("unsupported action type %q", actionType))
	}
}

func compileCondition(raw any, depth int, path string, totalNodes *int) (*ConditionNode, error) {
	if depth > MaxConditionDepth {
		return nil, validationError(path, fmt.Sprintf("condition depth %d exceeds maximum allowed %d", depth, MaxConditionDepth))
	}

	*totalNodes++
	if *totalNodes > MaxNodesPerRule {
		return nil, validationError(path, fmt.Sprintf("condition node count exceeds maximum allowed %d per rule", MaxNodesPerRule))
	}

	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, validationError(path, "condition node must be an object")
	}

	hasAll := obj["all"] != nil
	hasAny := obj["any"] != nil
	hasNot := obj["not"] != nil
	hasFact := obj["fact"] != nil
	hasPred := obj["predicate"] != nil

	formCount := 0
	for _, present := range []bool{hasAll, hasAny, hasNot, hasFact, hasPred} {
		if present {
			formCount++
		}
	}

	if formCount == 0 {
		return nil, validationError(path, "empty condition node object, must specify one of all, any, not, fact, predicate")
	}
	if formCount > 1 {
		return nil, validationError(path, "condition node must specify exactly one of all, any, not, fact, predicate")
	}

	if hasAll || hasAny {
		key := "all"
		kind := ConditionKindAll
		if hasAny {
			key = "any"
			kind = ConditionKindAny
		}
		rawList, ok := obj[key].([]any)
		if !ok {
			return nil, validationError(path+"."+key, fmt.Sprintf("'%s' field must be an array", key))
		}
		if len(rawList) == 0 {
			return nil, validationError(path+"."+key, fmt.Sprintf("'%s' array cannot be empty", key))
		}
		if len(rawList) > MaxListItems {
			return nil, validationError(path+"."+key, fmt.Sprintf("'%s' items count %d exceeds maximum %d", key, len(rawList), MaxListItems))
		}

		children := make([]*ConditionNode, 0, len(rawList))
		for i, item := range rawList {
			childPath := fmt.Sprintf("%s.%s[%d]", path, key, i)
			childNode, err := compileCondition(item, depth+1, childPath, totalNodes)
			if err != nil {
				return nil, err
			}
			children = append(children, childNode)
		}
		return &ConditionNode{Kind: kind, Children: children}, nil
	}

	if hasNot {
		childPath := path + ".not"
		childNode, err := compileCondition(obj["not"], depth+1, childPath, totalNodes)
		if err != nil {
			return nil, err
		}
		return &ConditionNode{Kind: ConditionKindNot, Child: childNode}, nil
	}

	if hasPred {
		predName, ok := obj["predicate"].(string)
		if !ok {
			return nil, validationError(path+".predicate", "predicate name must be string")
		}
		_, found := FindPredicate(predName)
		if !found {
			return nil, validationError(path+".predicate", fmt.Sprintf("unregistered predicate %q", predName))
		}

		switch predName {
		case "time_window":
			rawWeekdays, hasWk := obj["weekdays"].([]any)
			if !hasWk || len(rawWeekdays) == 0 {
				return nil, validationError(path+".weekdays", "time_window requires non-empty weekdays array")
			}
			if len(rawWeekdays) > 7 {
				return nil, validationError(path+".weekdays", "time_window weekdays array cannot have more than 7 elements")
			}

			seenWk := make(map[int]struct{})
			weekdays := make([]int, 0, len(rawWeekdays))
			for _, w := range rawWeekdays {
				wv, ok := w.(json.Number)
				if !ok {
					return nil, validationError(path+".weekdays", "weekday must be integer 0-6")
				}
				parsed, pErr := wv.Float64()
				if pErr != nil || parsed != math.Trunc(parsed) {
					return nil, validationError(path+".weekdays", "weekday must be integer 0-6")
				}
				if parsed < 0 || parsed > 6 {
					return nil, validationError(path+".weekdays", fmt.Sprintf("weekday %v out of range (0-6)", parsed))
				}
				wkInt := int(parsed)
				if _, exists := seenWk[wkInt]; exists {
					return nil, validationError(path+".weekdays", fmt.Sprintf("duplicate weekday %d", wkInt))
				}
				seenWk[wkInt] = struct{}{}
				weekdays = append(weekdays, wkInt)
			}

			rawRanges, hasRanges := obj["ranges"].([]any)
			if !hasRanges || len(rawRanges) == 0 {
				return nil, validationError(path+".ranges", "time_window requires non-empty ranges array")
			}
			if len(rawRanges) > MaxListItems {
				return nil, validationError(path+".ranges", fmt.Sprintf("ranges count %d exceeds maximum %d", len(rawRanges), MaxListItems))
			}

			timeRanges := make([]TimeRange, 0, len(rawRanges))
			for rIdx, rItem := range rawRanges {
				rangePair, ok := rItem.([]any)
				if !ok || len(rangePair) != 2 {
					return nil, validationError(fmt.Sprintf("%s.ranges[%d]", path, rIdx), "each time range must be an array of exactly 2 string elements [start, end]")
				}
				startStr, ok1 := rangePair[0].(string)
				endStr, ok2 := rangePair[1].(string)
				if !ok1 || !ok2 {
					return nil, validationError(fmt.Sprintf("%s.ranges[%d]", path, rIdx), "start and end times must be string 'HH:MM'")
				}
				tr, err := compileTimeRange(startStr, endStr)
				if err != nil {
					return nil, validationError(fmt.Sprintf("%s.ranges[%d]", path, rIdx), err.Error())
				}
				timeRanges = append(timeRanges, tr)
			}

			return &ConditionNode{
				Kind:     ConditionKindTimeWindow,
				Weekdays: weekdays,
				Ranges:   timeRanges,
			}, nil

		default:
			return nil, validationError(path+".predicate", fmt.Sprintf("unsupported predicate %q", predName))
		}
	}

	// fact
	factKey, ok := obj["fact"].(string)
	if !ok {
		return nil, validationError(path+".fact", "fact must be string")
	}
	desc, found := FindParam(factKey)
	if !found {
		return nil, validationError(path+".fact", fmt.Sprintf("unknown parameter %q", factKey))
	}

	rawOp, hasOp := obj["op"]
	if !hasOp {
		return nil, validationError(path+".op", "missing comparison operator 'op'")
	}
	opStr, ok := rawOp.(string)
	if !ok {
		return nil, validationError(path+".op", "comparison operator 'op' must be string")
	}

	if !slices.Contains(desc.Operators, opStr) {
		return nil, validationError(path+".op", fmt.Sprintf("operator %q is not supported for parameter %q", opStr, factKey))
	}

	rawValue, hasValue := obj["value"]
	if !hasValue {
		return nil, validationError(path+".value", "missing comparison 'value'")
	}

	isQuota := (desc.Key == "credential.quota.remaining_ratio")
	node := &ConditionNode{
		Kind:      ConditionKindParam,
		Fact:      factKey,
		ParamType: desc.Type,
		IsQuota:   isQuota,
		Op:        opStr,
	}

	// 编译 value 及参数专属字段
	switch desc.Type {
	case ParamTypeString:
		if opStr == "eq" {
			valStr, ok := rawValue.(string)
			if !ok {
				return nil, validationError(path+".value", "value must be string for operator eq")
			}
			if strings.TrimSpace(valStr) != valStr {
				return nil, validationError(path+".value", "string value cannot have leading or trailing whitespace")
			}
			if utf8.RuneCountInString(valStr) > MaxModelLength {
				return nil, validationError(path+".value", fmt.Sprintf("string value length %d exceeds maximum %d", utf8.RuneCountInString(valStr), MaxModelLength))
			}
			node.StrValue = valStr
		} else if opStr == "in" {
			rawArr, ok := rawValue.([]any)
			if !ok {
				return nil, validationError(path+".value", "value must be array for operator in")
			}
			if len(rawArr) == 0 {
				return nil, validationError(path+".value", "'in' value list cannot be empty")
			}
			if len(rawArr) > MaxListItems {
				return nil, validationError(path+".value", fmt.Sprintf("'in' items count %d exceeds maximum %d", len(rawArr), MaxListItems))
			}

			inValues, err := compileStringItems(rawArr, path+".value", "item", "'in' value list", true)
			if err != nil {
				return nil, err
			}
			node.InValues = inValues
		}

	case ParamTypeNumber:
		nv, ok := rawValue.(json.Number)
		if !ok {
			return nil, validationError(path+".value", "value must be number")
		}
		numVal, err := nv.Float64()
		if err != nil {
			return nil, validationError(path+".value", "numeric value is invalid")
		}

		if isQuota {
			if numVal < 0 || numVal > 1 {
				return nil, validationError(path+".value", fmt.Sprintf("quota ratio %s out of range [0.0, 1.0]", nv))
			}

			// 编译 select 与 reduce
			rawSel, hasSel := obj["select"]
			if !hasSel {
				return nil, validationError(path+".select", "quota parameter requires 'select' object")
			}
			selObj, ok := rawSel.(map[string]any)
			if !ok {
				return nil, validationError(path+".select", "'select' must be an object")
			}

			scopeStr, ok := selObj["scope"].(string)
			if !ok || scopeStr != "account" {
				return nil, validationError(path+".select.scope", "quota select.scope must be 'account'")
			}

			rawWin := selObj["window_seconds"]
			var winSec int
			switch wv := rawWin.(type) {
			case json.Number:
				parsed, pErr := strconv.Atoi(wv.String())
				if pErr != nil {
					return nil, validationError(path+".select.window_seconds", "window_seconds must be integer")
				}
				winSec = parsed
			default:
				return nil, validationError(path+".select.window_seconds", "window_seconds must be integer")
			}
			if winSec <= 0 {
				return nil, validationError(path+".select.window_seconds", fmt.Sprintf("window_seconds %d must be greater than 0", winSec))
			}

			rawRed, hasRed := obj["reduce"]
			if !hasRed {
				return nil, validationError(path+".reduce", "quota parameter requires 'reduce' field")
			}
			redStr, ok := rawRed.(string)
			if !ok || redStr != "min" {
				return nil, validationError(path+".reduce", "quota reducer must be 'min'")
			}

			node.Selector = &QuotaSelector{Scope: scopeStr, WindowSeconds: winSec}
		}

		node.NumValue = numVal
	}

	return node, nil
}
