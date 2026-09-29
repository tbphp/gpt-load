package bifrost

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

var convertedRequestFields = map[protocol.Protocol]map[string]bool{
	protocol.OpenAICompletions: sdkRequestFields(reflect.TypeFor[openai.OpenAIChatRequest]()),
	protocol.OpenAIResponses:   sdkRequestFields(reflect.TypeFor[openai.OpenAIResponsesRequest]()),
	protocol.Anthropic:         sdkRequestFields(reflect.TypeFor[anthropic.AnthropicMessageRequest]()),
	protocol.Gemini:            sdkRequestFields(reflect.TypeFor[gemini.GeminiGenerationRequest]()),
}

// sdkRequestFields 跟随 SDK 的输入结构识别普通字段，不另行维护参数转换表。
func sdkRequestFields(value reflect.Type) map[string]bool {
	fields := make(map[string]bool)
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			for name := range sdkRequestFields(field.Type) {
				fields[name] = true
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[strings.ToLower(name)] = true
	}
	return fields
}

func convertedParameterOverrides(spec execution.AttemptSpec) (map[string]any, error) {
	if spec.RouteMode != execution.RouteConverted || len(spec.ConfiguredParameters) == 0 {
		return nil, nil
	}
	known := convertedRequestFields[spec.ClientProtocol]
	if known == nil {
		return nil, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(spec.Body, &body); err != nil {
		return nil, fmt.Errorf("invalid overridden request body")
	}
	// 复用原生请求的控制字段清理，扩展参数不能接管凭据或 SDK 路由。
	stripNativeControlFields(body)
	extras := make(map[string]any)
	for _, name := range spec.ConfiguredParameters {
		lower := strings.ToLower(name)
		if known[lower] {
			continue
		}
		if spec.ClientProtocol == protocol.Gemini {
			// Gemini 的自定义解码器同时支持这些 snake_case 别名。
			switch lower {
			case "system_instruction", "generation_config", "safety_settings", "tool_config", "cached_content", "service_tier":
				continue
			}
		}
		switch strings.ReplaceAll(lower, "-", "_") {
		case "model", "stream", "store", "previous_response_id":
			continue
		}
		if raw, exists := body[name]; exists {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("invalid overridden parameter")
			}
			extras[name] = value
		}
	}
	return extras, nil
}

func mergeConvertedParameterOverrides(spec execution.AttemptSpec, params *map[string]any) error {
	extras, err := convertedParameterOverrides(spec)
	if err != nil {
		return err
	}
	if len(extras) == 0 {
		return nil
	}
	if *params == nil {
		*params = make(map[string]any)
	}
	for name, value := range extras {
		(*params)[name] = value
	}
	return nil
}
