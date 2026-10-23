package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// inspectAdvisorModels 返回 advisor 工具声明的模型。上游按这些模型执行并计费，
// 网关要像顶层 model 一样校验它们。键名按不区分大小写匹配（与 SDK 解析一致），
// 折叠后重复的键有歧义，直接拒绝。
func inspectAdvisorModels(body []byte) ([]string, error) {
	// JSON 只能用 \u 转义写出字母，两者都不出现时请求里不可能有 advisor 工具。
	if !bytes.Contains(body, []byte("advisor")) && !bytes.Contains(body, []byte(`\u`)) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("request body must be a JSON object")
	}
	var models []string
	toolsSeen := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode request field: %w", err)
		}
		field, _ := token.(string)
		if !strings.EqualFold(field, "tools") {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return nil, fmt.Errorf("decode request field %q: %w", field, err)
			}
			continue
		}
		if toolsSeen {
			return nil, fmt.Errorf("tools must be unique")
		}
		toolsSeen = true
		var tools []json.RawMessage
		if err := decoder.Decode(&tools); err != nil {
			return nil, fmt.Errorf("tools must be an array")
		}
		for _, tool := range tools {
			model, err := advisorToolModel(tool)
			if err != nil {
				return nil, err
			}
			if model != "" && !slices.Contains(models, model) {
				models = append(models, model)
			}
		}
	}
	return models, nil
}

// advisorToolModel 返回 advisor 工具的模型；其他工具返回空字符串，由上游校验。
func advisorToolModel(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", nil
	}
	var kind, model json.RawMessage
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("decode tool field: %w", err)
		}
		field, _ := token.(string)
		target := (*json.RawMessage)(nil)
		switch {
		case strings.EqualFold(field, "type"):
			target = &kind
		case strings.EqualFold(field, "model"):
			target = &model
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return "", fmt.Errorf("decode tool field %q: %w", field, err)
			}
			continue
		}
		if *target != nil {
			return "", fmt.Errorf("tool %s must be unique", strings.ToLower(field))
		}
		if err := decoder.Decode(target); err != nil {
			return "", fmt.Errorf("decode tool %s: %w", strings.ToLower(field), err)
		}
	}
	var toolType string
	if json.Unmarshal(kind, &toolType) != nil || !strings.HasPrefix(toolType, "advisor") {
		return "", nil
	}
	var advisorModel string
	if json.Unmarshal(model, &advisorModel) != nil || advisorModel == "" {
		return "", fmt.Errorf("advisor tool model must be a non-empty string")
	}
	return advisorModel, nil
}
