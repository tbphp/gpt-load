package codexrouting

import (
	"encoding/json"
	"regexp"
	"strings"
)

const candyPrompt = `不使用任何外部工具回答以下问题：

在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

        苹果味  桃子味  西瓜味
圆形       7      9      8
五角星形   7      6      4
`

// Independent "21" — same rule as haowang02/codex-candy-eval. RE2 has no lookbehind.
var candyAnswerPattern = regexp.MustCompile(`(?:^|[^0-9])21(?:[^0-9]|$)`)

func candyPassed(body []byte) bool {
	return candyAnswerPattern.MatchString(extractProbeText(body))
}

func extractProbeText(body []byte) string {
	var chunks []string
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue
		}
		if typ, _ := obj["type"].(string); typ == "response.output_text.delta" {
			if delta, ok := obj["delta"].(string); ok {
				chunks = append(chunks, delta)
			}
		}
		resp, _ := obj["response"].(map[string]any)
		if resp == nil {
			continue
		}
		output, _ := resp["output"].([]any)
		for _, item := range output {
			part, _ := item.(map[string]any)
			if part == nil {
				continue
			}
			content, _ := part["content"].([]any)
			for _, piece := range content {
				block, _ := piece.(map[string]any)
				if text, ok := block["text"].(string); ok {
					chunks = append(chunks, text)
				}
			}
			if text, ok := part["text"].(string); ok {
				chunks = append(chunks, text)
			}
		}
	}
	return strings.Join(chunks, "")
}

func candyPayload(model string) map[string]any {
	return map[string]any{
		"model":        model,
		"instructions": "Do not use tools. Answer the user question.",
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": candyPrompt},
				},
			},
		},
		"stream":              true,
		"store":               false,
		"reasoning":           map[string]any{"effort": "medium"},
		"tools":               []any{},
		"parallel_tool_calls": false,
	}
}
