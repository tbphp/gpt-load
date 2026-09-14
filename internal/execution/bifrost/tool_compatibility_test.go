package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/bedrock"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const convertedAllowedTools = `[{"type":"function","name":"lookup","description":"lookup description","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false},"strict":true},{"type":"function","name":"summarize","parameters":{"type":"object"},"strict":false}]`

func TestConvertedResponsesAllowedToolsPreserveTargetConstraint(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"auto", "required"} {
		for _, target := range []struct {
			name         string
			providerKind channel.ProviderKind
			provider     schemas.ModelProvider
			model        string
			wantTools    int
		}{
			{"Chat fallback", channel.ProviderOpenAICompatible, schemas.OpenAI, "compatible-model", 1},
			{"OpenAI Responses", channel.ProviderOpenAI, schemas.OpenAI, "gpt-5.2", 2},
			{"Anthropic", channel.ProviderAnthropic, schemas.Anthropic, "claude-sonnet-4-6", 1},
			{"Gemini", channel.ProviderGemini, schemas.Gemini, "gemini-2.5-pro", 2},
			{"Bedrock", channel.ProviderAWSBedrock, schemas.Bedrock, "anthropic.claude-sonnet-4-6", 1},
		} {
			t.Run(target.name+"/"+mode, func(t *testing.T) {
				spec, request := convertedResponsesAllowedToolsRequest(t, target.provider, target.model, mode,
					`[{"role":"user","content":"hello"}]`)
				prepared, failure := finishConvertedPreparation(spec, target.providerKind, preparedAttempt{responsesRequest: request})
				if failure != nil {
					t.Fatalf("valid function allowlist rejected: %+v", failure.Error)
				}
				wire := convertedToolTargetWire(t, target.providerKind, target.model, prepared.responsesRequest)
				if got := int(gjson.GetBytes(wire, convertedToolArrayPath(target.providerKind)+".#").Int()); got != target.wantTools {
					t.Fatalf("target tools = %d, want %d: %s", got, target.wantTools, wire)
				}
				assertAllowedToolTargetMode(t, wire, target.providerKind, mode)
				if target.wantTools == 1 && !targetHasOnlyLookupTool(target.providerKind, wire) {
					t.Fatalf("target allowlist was widened: %s", wire)
				}
				if target.providerKind == channel.ProviderOpenAICompatible &&
					(gjson.GetBytes(wire, "tools.0.function.description").String() != "lookup description" ||
						gjson.GetBytes(wire, "tools.0.function.parameters.properties.query.type").String() != "string" ||
						!gjson.GetBytes(wire, "tools.0.function.strict").Bool() ||
						!gjson.GetBytes(wire, "parallel_tool_calls").Exists() || gjson.GetBytes(wire, "parallel_tool_calls").Bool()) {
					t.Fatalf("Chat function definition or parallel mode changed: %s", wire)
				}
			})
		}
	}
}

func TestConvertedOpenAIChatAllowedToolsPreserveTargetConstraint(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"auto", "required"} {
		for _, target := range []struct {
			name         string
			providerKind channel.ProviderKind
			provider     schemas.ModelProvider
			model        string
		}{
			{"Anthropic", channel.ProviderAnthropic, schemas.Anthropic, "claude-sonnet-4-6"},
			{"Gemini", channel.ProviderGemini, schemas.Gemini, "gemini-2.5-pro"},
			{"Bedrock", channel.ProviderAWSBedrock, schemas.Bedrock, "anthropic.claude-sonnet-4-6"},
		} {
			t.Run(target.name+"/"+mode, func(t *testing.T) {
				request := convertedChatAllowedToolsRequest(t, target.provider, target.model, mode)
				spec := execution.AttemptSpec{
					ClientProtocol: protocol.OpenAICompletions,
					RouteMode:      execution.RouteConverted,
					Operation:      execution.OperationChatCompletion,
					UpstreamModel:  target.model,
				}
				prepared, failure := finishConvertedPreparation(spec, target.providerKind, preparedAttempt{request: request})
				if failure != nil {
					t.Fatalf("valid Chat function allowlist rejected: %+v", failure.Error)
				}
				wire := convertedChatToolTargetWire(t, target.providerKind, prepared.request)
				if got := int(gjson.GetBytes(wire, convertedToolArrayPath(target.providerKind)+".#").Int()); got != 1 {
					t.Fatalf("target tools = %d, want 1: %s", got, wire)
				}
				assertAllowedToolTargetMode(t, wire, target.providerKind, mode)
				if !targetHasOnlyLookupTool(target.providerKind, wire) {
					t.Fatalf("target allowlist was widened: %s", wire)
				}
			})
		}
	}
}

func TestConvertedAllowedToolsRejectInvalidConstraints(t *testing.T) {
	t.Parallel()
	for _, target := range []channel.ProviderKind{
		channel.ProviderOpenAICompatible,
		channel.ProviderAnthropic,
		channel.ProviderAWSBedrock,
	} {
		for _, choice := range []string{
			`{"type":"allowed_tools","mode":"auto","tools":[]}`,
			`{"type":"allowed_tools","mode":"required"}`,
			`{"type":"allowed_tools","mode":"none","tools":[{"type":"function","name":"lookup"}]}`,
			`{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"missing"}]}`,
			`{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"lookup"}]}`,
			`{"type":"allowed_tools","mode":"required","tools":[{"type":"mcp","server_label":"docs"}]}`,
		} {
			t.Run(fmt.Sprintf("%s/%s", target, choice), func(t *testing.T) {
				provider, model := convertedToolProvider(target)
				body := []byte(fmt.Sprintf(`{"model":"client-model","input":"hello","store":false,"tools":%s,"tool_choice":%s}`, convertedAllowedTools, choice))
				spec := execution.AttemptSpec{
					ClientProtocol: protocol.OpenAIResponses,
					RouteMode:      execution.RouteConverted,
					Operation:      execution.OperationResponsesCreate,
					ClientModel:    "client-model",
					UpstreamModel:  model,
					Body:           body,
				}
				request, err := buildConvertedResponsesRequest(spec, provider)
				if err != nil {
					t.Fatal(err)
				}
				_, failure := finishConvertedPreparation(spec, target, preparedAttempt{responsesRequest: request})
				if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
					failure.Error.Kind != execution.ErrorKindConversionUnsupported || failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
					t.Fatalf("invalid allowlist was not rejected before dispatch: %+v", failure)
				}
			})
		}
	}
}

func TestConvertedOpenAIChatAllowedToolsRejectInvalidConstraints(t *testing.T) {
	t.Parallel()
	for _, choice := range []string{
		`{"type":"allowed_tools"}`,
		`{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[]}}`,
		`{"type":"allowed_tools","allowed_tools":{"mode":"none","tools":[{"type":"function","function":{"name":"lookup"}}]}}`,
		`{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"missing"}}]}}`,
	} {
		t.Run(choice, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":%s}`, choice))
			var wire openai.OpenAIChatRequest
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatal(err)
			}
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			defer ctx.Cancel()
			request := wire.ToBifrostChatRequest(ctx)
			request.Provider, request.Model = schemas.Anthropic, "claude-sonnet-4-6"
			spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAICompletions, RouteMode: execution.RouteConverted, Operation: execution.OperationChatCompletion, UpstreamModel: request.Model}
			_, failure := finishConvertedPreparation(spec, channel.ProviderAnthropic, preparedAttempt{request: request})
			if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
				failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
				t.Fatalf("invalid Chat allowlist was not rejected: %+v", failure)
			}
		})
	}
}

func TestChatFallbackRejectsOnlyCallableUnsupportedTools(t *testing.T) {
	t.Parallel()
	const tools = `[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"web_search_preview"}]`
	for _, test := range []struct {
		name      string
		choice    string
		wantError bool
		wantMode  string
	}{
		{"disabled", `"none"`, false, "none"},
		{"selected function", `{"type":"function","name":"lookup"}`, false, "function"},
		{"automatic", `"auto"`, true, ""},
		{"required from all tools", `"required"`, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"client-model","input":"hello","store":false,"tools":%s,"tool_choice":%s}`, tools, test.choice))
			spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIResponses, RouteMode: execution.RouteConverted, Operation: execution.OperationResponsesCreate, ClientModel: "client-model", UpstreamModel: "compatible-model", Body: body}
			request, err := buildConvertedResponsesRequest(spec, schemas.OpenAI)
			if err != nil {
				t.Fatal(err)
			}
			prepared, failure := finishConvertedPreparation(spec, channel.ProviderGroq, preparedAttempt{responsesRequest: request})
			if test.wantError {
				if failure == nil || failure.DispatchState != execution.DispatchNotSent {
					t.Fatalf("callable unsupported tool was not rejected: %+v", failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("inactive tool caused rejection: %+v", failure.Error)
			}
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			defer ctx.Cancel()
			wire, err := json.Marshal(openai.ToOpenAIChatRequest(ctx, prepared.responsesRequest.ToChatRequest()))
			if err != nil {
				t.Fatal(err)
			}
			if gjson.GetBytes(wire, "tools.#").Int() != 1 || gjson.GetBytes(wire, "tools.0.function.name").String() != "lookup" {
				t.Fatalf("convertible function was not preserved: %s", wire)
			}
			if test.wantMode == "function" {
				if gjson.GetBytes(wire, "tool_choice.type").String() != "function" || gjson.GetBytes(wire, "tool_choice.function.name").String() != "lookup" {
					t.Fatalf("selected function changed: %s", wire)
				}
			} else if gjson.GetBytes(wire, "tool_choice").String() != test.wantMode {
				t.Fatalf("tool mode changed: %s", wire)
			}
		})
	}
}

func TestConvertedToolModesUseTargetEquivalentValues(t *testing.T) {
	t.Parallel()
	t.Run("Anthropic any becomes Chat required", func(t *testing.T) {
		mode := "any"
		request := &schemas.BifrostResponsesRequest{Params: &schemas.ResponsesParameters{
			Tools: []schemas.ResponsesTool{{
				Type:                  schemas.ResponsesToolTypeFunction,
				Name:                  schemas.Ptr("lookup"),
				ResponsesToolFunction: &schemas.ResponsesToolFunction{},
			}},
			ToolChoice: &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: &mode},
		}}
		spec := execution.AttemptSpec{ClientProtocol: protocol.Anthropic, RouteMode: execution.RouteConverted, Operation: execution.OperationChatCompletion}
		prepared, failure := finishConvertedPreparation(spec, channel.ProviderOpenAICompatible, preparedAttempt{responsesRequest: request})
		if failure != nil {
			t.Fatalf("required tool mode rejected: %+v", failure.Error)
		}
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		defer ctx.Cancel()
		wire, err := json.Marshal(openai.ToOpenAIChatRequest(ctx, prepared.responsesRequest.ToChatRequest()))
		if err != nil {
			t.Fatal(err)
		}
		if got := gjson.GetBytes(wire, "tool_choice").String(); got != "required" {
			t.Fatalf("Chat tool_choice = %q, want required: %s", got, wire)
		}
	})

	for _, test := range []struct {
		geminiMode string
		wantType   string
		wantMode   string
	}{
		{"AUTO", "allowed_tools", "auto"},
		{"ANY", "function", ""},
	} {
		t.Run("Gemini "+test.geminiMode+" allowlist becomes valid OpenAI Responses choice", func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}},{"name":"summarize","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":%q,"allowedFunctionNames":["lookup"]}}}`, test.geminiMode))
			spec := execution.AttemptSpec{
				ClientProtocol: protocol.Gemini,
				RouteMode:      execution.RouteConverted,
				Operation:      execution.OperationChatCompletion,
				ClientModel:    "client-model",
				UpstreamModel:  "gpt-5.2",
				Body:           body,
			}
			request, err := buildConvertedResponsesRequest(spec, schemas.OpenAI)
			if err != nil {
				t.Fatal(err)
			}
			prepared, failure := finishConvertedPreparation(spec, channel.ProviderOpenAI, preparedAttempt{responsesRequest: request})
			if failure != nil {
				t.Fatalf("valid Gemini allowlist rejected: %+v", failure.Error)
			}
			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			defer ctx.Cancel()
			wire, err := json.Marshal(openai.ToOpenAIResponsesRequest(ctx, prepared.responsesRequest))
			if err != nil {
				t.Fatal(err)
			}
			choice := gjson.GetBytes(wire, "tool_choice")
			if choice.Get("type").String() != test.wantType ||
				(test.wantMode != "" && choice.Get("mode").String() != test.wantMode) ||
				(test.wantType == "function" && choice.Get("name").String() != "lookup") ||
				gjson.GetBytes(wire, "tools.#").Int() != 2 {
				t.Fatalf("OpenAI Responses allowlist is invalid: %s", wire)
			}
		})
	}
}

func TestConvertedAllowedToolsKeepsDeepSeekThinkingProtection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		mode      string
		wantError bool
	}{
		{"auto", false},
		{"required", true},
	} {
		t.Run(test.mode, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"client-model","input":"hello","store":false,"reasoning":{"effort":"high"},"tools":%s,"tool_choice":{"type":"allowed_tools","mode":%q,"tools":[{"type":"function","name":"lookup"}]}}`, convertedAllowedTools, test.mode))
			spec := execution.AttemptSpec{
				ClientProtocol: protocol.OpenAIResponses,
				RouteMode:      execution.RouteConverted,
				Operation:      execution.OperationResponsesCreate,
				ClientModel:    "client-model",
				UpstreamModel:  "deepseek-chat",
				Body:           body,
			}
			request, err := buildConvertedResponsesRequest(spec, schemas.DeepSeek)
			if err != nil {
				t.Fatal(err)
			}
			prepared, failure := finishConvertedPreparation(spec, channel.ProviderDeepSeek, preparedAttempt{responsesRequest: request})
			if test.wantError {
				if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
					failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
					t.Fatalf("explicit thinking conflict was not rejected: %+v", failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("automatic tools changed thinking compatibility: %+v", failure.Error)
			}
			if prepared.responsesRequest.Params.Reasoning == nil || prepared.responsesRequest.Params.Reasoning.Effort == nil ||
				*prepared.responsesRequest.Params.Reasoning.Effort != "high" ||
				prepared.responsesRequest.Params.ToolChoice == nil || prepared.responsesRequest.Params.ToolChoice.ResponsesToolChoiceStr == nil ||
				*prepared.responsesRequest.Params.ToolChoice.ResponsesToolChoiceStr != "auto" {
				t.Fatalf("thinking or automatic tool mode changed: %#v", prepared.responsesRequest.Params)
			}
		})
	}
}

func TestConvertedAllowedToolsKeepsParallelToolCallTriState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		field string
		want  *bool
	}{
		{"unset", "", nil},
		{"disabled", `,"parallel_tool_calls":false`, schemas.Ptr(false)},
		{"enabled", `,"parallel_tool_calls":true`, schemas.Ptr(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"client-model","input":"hello","store":false%s,"tools":%s,"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`, test.field, convertedAllowedTools))
			spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIResponses, RouteMode: execution.RouteConverted, Operation: execution.OperationResponsesCreate, ClientModel: "client-model", UpstreamModel: "claude-sonnet-4-6", Body: body}
			request, err := buildConvertedResponsesRequest(spec, schemas.Anthropic)
			if err != nil {
				t.Fatal(err)
			}
			prepared, failure := finishConvertedPreparation(spec, channel.ProviderAnthropic, preparedAttempt{responsesRequest: request})
			if failure != nil {
				t.Fatalf("valid allowlist rejected: %+v", failure.Error)
			}
			got := prepared.responsesRequest.Params.ParallelToolCalls
			if (got == nil) != (test.want == nil) || got != nil && *got != *test.want {
				t.Fatalf("parallel_tool_calls = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConvertedToolCompatibilityPreservesHistoryAndIsolation(t *testing.T) {
	t.Parallel()
	t.Run("legal empty function result", func(t *testing.T) {
		input := `[{"role":"user","content":"start"},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"x\"}"},{"type":"function_call_output","call_id":"call_1","output":""},{"role":"user","content":"continue"}]`
		spec, request := convertedResponsesAllowedToolsRequest(t, schemas.OpenAI, "compatible-model", "required", input)
		prepared, failure := finishConvertedPreparation(spec, channel.ProviderOpenAICompatible, preparedAttempt{responsesRequest: request})
		if failure != nil {
			t.Fatalf("legal function history rejected: %+v", failure.Error)
		}
		chat := prepared.responsesRequest.ToChatRequest()
		if len(chat.Input) != 4 || chat.Input[1].ChatAssistantMessage == nil || len(chat.Input[1].ToolCalls) != 1 ||
			chat.Input[2].ChatToolMessage == nil || chat.Input[2].Content == nil || chat.Input[2].Content.ContentStr == nil ||
			*chat.Input[2].Content.ContentStr != "" || *chat.Input[2].ChatToolMessage.ToolCallID != "call_1" {
			t.Fatalf("function history was not preserved: %#v", chat.Input)
		}
	})

	t.Run("unsupported custom history", func(t *testing.T) {
		input := `[{"role":"user","content":"start"},{"type":"custom_tool_call","call_id":"call_1","name":"custom","input":"synthetic"},{"role":"user","content":"continue"}]`
		spec, request := convertedResponsesAllowedToolsRequest(t, schemas.OpenAI, "compatible-model", "required", input)
		_, failure := finishConvertedPreparation(spec, channel.ProviderOpenAICompatible, preparedAttempt{responsesRequest: request})
		if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
			failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
			t.Fatalf("unsupported tool history was not rejected: %+v", failure)
		}
	})

	for _, target := range []struct {
		name         string
		providerKind channel.ProviderKind
		provider     schemas.ModelProvider
		model        string
	}{
		{"Anthropic", channel.ProviderAnthropic, schemas.Anthropic, "claude-sonnet-4-6"},
		{"Bedrock", channel.ProviderAWSBedrock, schemas.Bedrock, "anthropic.claude-sonnet-4-6"},
	} {
		t.Run(target.name+" rejects downgraded custom history", func(t *testing.T) {
			input := `[{"role":"user","content":"start"},{"type":"custom_tool_call","call_id":"call_1","name":"custom","input":"synthetic"},{"role":"user","content":"continue"}]`
			spec, request := convertedResponsesAllowedToolsRequest(t, target.provider, target.model, "required", input)
			_, failure := finishConvertedPreparation(spec, target.providerKind, preparedAttempt{responsesRequest: request})
			if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
				failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
				t.Fatalf("downgraded custom history was not rejected: %+v", failure)
			}
		})
	}

	for _, target := range []struct {
		name         string
		providerKind channel.ProviderKind
		provider     schemas.ModelProvider
		model        string
	}{
		{"Chat fallback", channel.ProviderOpenAICompatible, schemas.OpenAI, "compatible-model"},
		{"Anthropic", channel.ProviderAnthropic, schemas.Anthropic, "claude-sonnet-4-6"},
		{"Bedrock", channel.ProviderAWSBedrock, schemas.Bedrock, "anthropic.claude-sonnet-4-6"},
	} {
		t.Run(target.name+" rejects compaction history", func(t *testing.T) {
			input := `[{"role":"user","content":"start"},{"type":"compaction","encrypted_content":"opaque-context"},{"role":"user","content":"continue"}]`
			spec, request := convertedResponsesAllowedToolsRequest(t, target.provider, target.model, "required", input)
			_, failure := finishConvertedPreparation(spec, target.providerKind, preparedAttempt{responsesRequest: request})
			if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
				failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
				t.Fatalf("compaction history was not rejected: %+v", failure)
			}
		})
	}

	for _, choice := range []string{`"none"`, `{"type":"function","name":"lookup"}`} {
		t.Run("newly allowed branch rejects custom history "+choice, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"client-model","input":[{"role":"user","content":"start"},{"type":"custom_tool_call","call_id":"call_1","name":"custom","input":"synthetic"}],"store":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"custom","name":"custom","format":{"type":"text"}}],"tool_choice":%s}`, choice))
			spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIResponses, RouteMode: execution.RouteConverted, Operation: execution.OperationResponsesCreate, ClientModel: "client-model", UpstreamModel: "compatible-model", Body: body}
			request, err := buildConvertedResponsesRequest(spec, schemas.OpenAI)
			if err != nil {
				t.Fatal(err)
			}
			_, failure := finishConvertedPreparation(spec, channel.ProviderOpenAICompatible, preparedAttempt{responsesRequest: request})
			if failure == nil || failure.DispatchState != execution.DispatchNotSent || failure.Error == nil ||
				failure.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
				t.Fatalf("unsupported custom history was not rejected: %+v", failure)
			}
		})
	}

	t.Run("attempt-local request copy", func(t *testing.T) {
		spec, request := convertedResponsesAllowedToolsRequest(t, schemas.Anthropic, "claude-sonnet-4-6", "required",
			`[{"role":"user","content":"hello"}]`)
		originalChoice, err := json.Marshal(request.Params.ToolChoice)
		if err != nil {
			t.Fatal(err)
		}
		prepared, failure := finishConvertedPreparation(spec, channel.ProviderAnthropic, preparedAttempt{responsesRequest: request})
		if failure != nil {
			t.Fatalf("valid allowlist rejected: %+v", failure.Error)
		}
		if prepared.responsesRequest == request || prepared.responsesRequest.Params == request.Params ||
			len(request.Params.Tools) != 2 || len(prepared.responsesRequest.Params.Tools) != 1 {
			t.Fatalf("tool adaptation was not isolated: original=%p/%p prepared=%p/%p", request, request.Params, prepared.responsesRequest, prepared.responsesRequest.Params)
		}
		currentChoice, err := json.Marshal(request.Params.ToolChoice)
		if err != nil {
			t.Fatal(err)
		}
		if string(currentChoice) != string(originalChoice) {
			t.Fatalf("original choice mutated: got %s want %s", currentChoice, originalChoice)
		}
	})
}

func convertedResponsesAllowedToolsRequest(
	t *testing.T,
	provider schemas.ModelProvider,
	model string,
	mode string,
	input string,
) (execution.AttemptSpec, *schemas.BifrostResponsesRequest) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"model":"client-model","input":%s,"store":false,"parallel_tool_calls":false,"tools":%s,"tool_choice":{"type":"allowed_tools","mode":%q,"tools":[{"type":"function","name":"lookup"}]}}`, input, convertedAllowedTools, mode))
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIResponses,
		RouteMode:      execution.RouteConverted,
		Operation:      execution.OperationResponsesCreate,
		ClientModel:    "client-model",
		UpstreamModel:  model,
		Body:           body,
	}
	request, err := buildConvertedResponsesRequest(spec, provider)
	if err != nil {
		t.Fatal(err)
	}
	request.Model = model
	return spec, request
}

func convertedChatAllowedToolsRequest(t *testing.T, provider schemas.ModelProvider, model string, mode string) *schemas.BifrostChatRequest {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"parallel_tool_calls":false,"tools":[{"type":"function","function":{"name":"lookup","description":"lookup description","parameters":{"type":"object"},"strict":true}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"},"strict":false}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":%q,"tools":[{"type":"function","function":{"name":"lookup"}}]}}}`, mode))
	var wire openai.OpenAIChatRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	request := wire.ToBifrostChatRequest(ctx)
	request.Provider = provider
	request.Model = model
	return request
}

func convertedToolProvider(kind channel.ProviderKind) (schemas.ModelProvider, string) {
	switch kind {
	case channel.ProviderAnthropic:
		return schemas.Anthropic, "claude-sonnet-4-6"
	case channel.ProviderGemini:
		return schemas.Gemini, "gemini-2.5-pro"
	case channel.ProviderAWSBedrock:
		return schemas.Bedrock, "anthropic.claude-sonnet-4-6"
	default:
		return schemas.OpenAI, "compatible-model"
	}
}

func convertedToolTargetWire(t *testing.T, kind channel.ProviderKind, model string, request *schemas.BifrostResponsesRequest) []byte {
	t.Helper()
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	request.Model = model
	var value any
	var err error
	switch kind {
	case channel.ProviderOpenAICompatible:
		value = openai.ToOpenAIChatRequest(ctx, request.ToChatRequest())
	case channel.ProviderOpenAI:
		value = openai.ToOpenAIResponsesRequest(ctx, request)
	case channel.ProviderAnthropic:
		value, err = anthropic.ToAnthropicResponsesRequest(ctx, request)
	case channel.ProviderGemini:
		value, err = gemini.ToGeminiResponsesRequest(ctx, request)
	case channel.ProviderAWSBedrock:
		value, err = bedrock.ToBedrockResponsesRequest(ctx, request)
	default:
		t.Fatalf("unsupported test provider %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func convertedChatToolTargetWire(t *testing.T, kind channel.ProviderKind, request *schemas.BifrostChatRequest) []byte {
	t.Helper()
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	var value any
	var err error
	switch kind {
	case channel.ProviderAnthropic:
		value, err = anthropic.ToAnthropicChatRequest(ctx, request)
	case channel.ProviderGemini:
		value, err = gemini.ToGeminiChatCompletionRequest(ctx, request)
	case channel.ProviderAWSBedrock:
		value, err = bedrock.ToBedrockChatCompletionRequest(ctx, request)
	default:
		t.Fatalf("unsupported test provider %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func convertedToolArrayPath(kind channel.ProviderKind) string {
	if kind == channel.ProviderAWSBedrock {
		return "toolConfig.tools"
	}
	if kind == channel.ProviderGemini {
		return "tools.0.functionDeclarations"
	}
	return "tools"
}

func targetHasOnlyLookupTool(kind channel.ProviderKind, wire []byte) bool {
	path := convertedToolArrayPath(kind)
	namePath := path + ".0.name"
	if kind == channel.ProviderOpenAICompatible {
		namePath = path + ".0.function.name"
	} else if kind == channel.ProviderAWSBedrock {
		namePath = path + ".0.toolSpec.name"
	}
	return gjson.GetBytes(wire, namePath).String() == "lookup"
}

func assertAllowedToolTargetMode(t *testing.T, wire []byte, kind channel.ProviderKind, mode string) {
	t.Helper()
	switch kind {
	case channel.ProviderOpenAICompatible:
		if got := gjson.GetBytes(wire, "tool_choice").String(); got != mode {
			t.Fatalf("Chat tool_choice = %q, want %q: %s", got, mode, wire)
		}
	case channel.ProviderAnthropic:
		want := mode
		if want == "required" {
			want = "any"
		}
		if got := gjson.GetBytes(wire, "tool_choice.type").String(); got != want {
			t.Fatalf("Anthropic tool_choice = %q, want %q: %s", got, want, wire)
		}
	case channel.ProviderGemini:
		want := "AUTO"
		if mode == "required" {
			want = "ANY"
		}
		allowed := gjson.GetBytes(wire, "toolConfig.functionCallingConfig.allowedFunctionNames")
		if got := gjson.GetBytes(wire, "toolConfig.functionCallingConfig.mode").String(); got != want ||
			(allowed.Exists() && gjson.GetBytes(wire, "toolConfig.functionCallingConfig.allowedFunctionNames.0").String() != "lookup") {
			t.Fatalf("Gemini allowlist or mode mismatch: %s", wire)
		}
	case channel.ProviderAWSBedrock:
		path := "toolConfig.toolChoice.auto"
		if mode == "required" {
			path = "toolConfig.toolChoice.any"
		}
		if mode == "required" && !gjson.GetBytes(wire, path).Exists() {
			t.Fatalf("Bedrock tool choice %q is missing: %s", mode, wire)
		}
	case channel.ProviderOpenAI:
		if gjson.GetBytes(wire, "tool_choice.type").String() != "allowed_tools" ||
			gjson.GetBytes(wire, "tool_choice.mode").String() != mode ||
			gjson.GetBytes(wire, "tool_choice.tools.0.name").String() != "lookup" {
			t.Fatalf("OpenAI Responses allowlist mismatch: %s", wire)
		}
	}
}
