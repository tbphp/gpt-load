package bifrost

import (
	"fmt"
	"strings"
	"sync"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/azure"
	"github.com/maximhq/bifrost/core/providers/bedrock"
	"github.com/maximhq/bifrost/core/providers/deepseek"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/groq"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/providers/openrouter"
	"github.com/maximhq/bifrost/core/providers/vertex"
	"github.com/maximhq/bifrost/core/providers/xai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
)

type sdkProviderInitializer func(*schemas.ProviderConfig, schemas.Logger) error

type sdkProviderSpec struct {
	provider   schemas.ModelProvider
	initialize sdkProviderInitializer

	defaultOnce    sync.Once
	defaultBaseURL string
	defaultErr     error
}

var sdkProviderSpecs = map[channel.ProviderKind]*sdkProviderSpec{
	channel.ProviderOpenAI: {
		provider: schemas.OpenAI,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			openai.NewOpenAIProvider(config, logger)
			return nil
		},
	},
	channel.ProviderAnthropic: {
		provider: schemas.Anthropic,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			anthropic.NewAnthropicProvider(config, logger)
			return nil
		},
	},
	channel.ProviderGemini: {
		provider: schemas.Gemini,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			gemini.NewGeminiProvider(config, logger)
			return nil
		},
	},
	channel.ProviderAzureOpenAI: {
		provider: schemas.Azure,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := azure.NewAzureProvider(config, logger)
			return err
		},
	},
	channel.ProviderAWSBedrock: {
		provider: schemas.Bedrock,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := bedrock.NewBedrockProvider(config, logger)
			return err
		},
	},
	channel.ProviderGoogleVertex: {
		provider: schemas.Vertex,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := vertex.NewVertexProvider(config, logger)
			return err
		},
	},
	channel.ProviderDeepSeek: {
		provider: schemas.DeepSeek,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := deepseek.NewDeepSeekProvider(config, logger)
			return err
		},
	},
	channel.ProviderOpenRouter: {
		provider: schemas.OpenRouter,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			openrouter.NewOpenRouterProvider(config, logger)
			return nil
		},
	},
	channel.ProviderGroq: {
		provider: schemas.Groq,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := groq.NewGroqProvider(config, logger)
			return err
		},
	},
	channel.ProviderXAI: {
		provider: schemas.XAI,
		initialize: func(config *schemas.ProviderConfig, logger schemas.Logger) error {
			_, err := xai.NewXAIProvider(config, logger)
			return err
		},
	},
}

func sdkProviderSpecFor(kind channel.ProviderKind) (*sdkProviderSpec, bool) {
	spec, ok := sdkProviderSpecs[kind]
	return spec, ok
}

func sdkDefaultBaseURL(kind channel.ProviderKind) (string, bool, error) {
	spec, ok := sdkProviderSpecFor(kind)
	if !ok {
		return "", false, nil
	}
	spec.defaultOnce.Do(func() {
		config := &schemas.ProviderConfig{
			ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
				Concurrency: 1,
				BufferSize:  1,
			},
		}
		if err := spec.initialize(config, bifrostcore.NewNoOpLogger()); err != nil {
			spec.defaultErr = fmt.Errorf("initialize SDK provider %q: %w", kind, err)
			return
		}
		spec.defaultBaseURL = strings.TrimSpace(config.NetworkConfig.BaseURL)
	})
	if spec.defaultErr != nil {
		return "", false, spec.defaultErr
	}
	return spec.defaultBaseURL, spec.defaultBaseURL != "", nil
}

// DefaultBaseURL returns the URL that the locked SDK writes into an empty
// channel BaseURL. Providers whose endpoint depends on channel parameters do
// not expose a unique default.
func (manager *RuntimeManager) DefaultBaseURL(channelID channel.ID) (string, bool, error) {
	if manager == nil || manager.registry == nil {
		return "", false, fmt.Errorf("read SDK default base URL: runtime manager is unavailable")
	}
	kind, ok := manager.registry.ProviderKind(channelID)
	if !ok {
		return "", false, fmt.Errorf("read SDK default base URL: unknown channel %q", channelID)
	}
	return sdkDefaultBaseURL(kind)
}
