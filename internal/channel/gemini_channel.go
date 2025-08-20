package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"
	"gpt-load/internal/channel/gemini"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func init() {
	Register("gemini", newGeminiChannel)
}

type GeminiChannel struct {
	*BaseChannel

	// Gemini 专用增强功能
	streamProcessor   *gemini.StreamProcessor
	configManager     *gemini.ConfigManager
	logger           *logrus.Logger

	// 初始化状态
	mutex            sync.RWMutex
	initialized      bool
}

func newGeminiChannel(f *Factory, group *models.Group) (ChannelProxy, error) {
	base, err := f.newBaseChannel("gemini", group)
	if err != nil {
		return nil, err
	}

	// 创建 Gemini 频道实例
	channel := &GeminiChannel{
		BaseChannel: base,
		logger:      logrus.WithField("channel", "gemini"),
		initialized: false,
	}

	// 延迟初始化 Gemini 专用组件
	if err := channel.initializeGeminiComponents(group); err != nil {
		logrus.WithError(err).Error("Failed to initialize Gemini components")
		// 不返回错误，允许基础功能正常工作
	}

	return channel, nil
}

// initializeGeminiComponents 初始化 Gemini 专用组件
func (ch *GeminiChannel) initializeGeminiComponents(group *models.Group) error {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()

	if ch.initialized {
		return nil
	}

	// 创建配置管理器
	ch.configManager = gemini.NewConfigManager(ch.logger)

	// 获取 Gemini 配置
	config := ch.configManager.GetConfig()

	// 创建流处理器
	ch.streamProcessor = gemini.NewStreamProcessor(config, ch.logger)

	// 验证配置
	if err := ch.streamProcessor.ValidateConfiguration(); err != nil {
		return fmt.Errorf("invalid Gemini configuration: %w", err)
	}

	ch.initialized = true
	ch.logger.Info("Gemini enhanced components initialized successfully")

	return nil
}

// ModifyRequest adds the API key as a query parameter for Gemini requests.
func (ch *GeminiChannel) ModifyRequest(req *http.Request, apiKey *models.APIKey, group *models.Group) {
	if strings.Contains(req.URL.Path, "v1beta/openai") {
		req.Header.Set("Authorization", "Bearer "+apiKey.KeyValue)
	} else {
		q := req.URL.Query()
		q.Set("key", apiKey.KeyValue)
		req.URL.RawQuery = q.Encode()
	}
}

// IsStreamRequest checks if the request is for a streaming response.
func (ch *GeminiChannel) IsStreamRequest(c *gin.Context, bodyBytes []byte) bool {
	path := c.Request.URL.Path
	if strings.HasSuffix(path, ":streamGenerateContent") {
		return true
	}

	// Also check for standard streaming indicators as a fallback.
	if strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
		return true
	}
	if c.Query("stream") == "true" {
		return true
	}

	type streamPayload struct {
		Stream bool `json:"stream"`
	}
	var p streamPayload
	if err := json.Unmarshal(bodyBytes, &p); err == nil {
		return p.Stream
	}

	return false
}

func (ch *GeminiChannel) ExtractModel(c *gin.Context, bodyBytes []byte) string {
	// gemini format
	path := c.Request.URL.Path
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "models" && i+1 < len(parts) {
			modelPart := parts[i+1]
			return strings.Split(modelPart, ":")[0]
		}
	}

	// openai format
	type modelPayload struct {
		Model string `json:"model"`
	}
	var p modelPayload
	if err := json.Unmarshal(bodyBytes, &p); err == nil && p.Model != "" {
		return strings.TrimPrefix(p.Model, "models/")
	}

	return ""
}

// ValidateKey checks if the given API key is valid by making a generateContent request.
func (ch *GeminiChannel) ValidateKey(ctx context.Context, apiKey *models.APIKey, group *models.Group) (bool, error) {
	upstreamURL := ch.getUpstreamURL()
	if upstreamURL == nil {
		return false, fmt.Errorf("no upstream URL configured for channel %s", ch.Name)
	}

	// Safely join the path segments
	reqURL, err := url.JoinPath(upstreamURL.String(), "v1beta", "models", ch.TestModel+":generateContent")
	if err != nil {
		return false, fmt.Errorf("failed to create gemini validation path: %w", err)
	}
	reqURL += "?key=" + apiKey.KeyValue

	payload := gin.H{
		"contents": []gin.H{
			{"parts": []gin.H{
				{"text": "hi"},
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal validation payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create validation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Apply custom header rules if available
	if len(group.HeaderRuleList) > 0 {
		headerCtx := utils.NewHeaderVariableContext(group, apiKey)
		utils.ApplyHeaderRules(req, group.HeaderRuleList, headerCtx)
	}

	resp, err := ch.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send validation request: %w", err)
	}
	defer resp.Body.Close()

	// Any 2xx status code indicates the key is valid.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}

	// For non-200 responses, parse the body to provide a more specific error reason.
	errorBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("key is invalid (status %d), but failed to read error body: %w", resp.StatusCode, err)
	}

	// Use the new parser to extract a clean error message.
	parsedError := app_errors.ParseUpstreamError(errorBody)

	return false, fmt.Errorf("[status %d] %s", resp.StatusCode, parsedError)
}

// ==================== Gemini 增强功能方法 ====================

// ProcessStreamWithRetry 使用智能重试处理流式响应
func (ch *GeminiChannel) ProcessStreamWithRetry(
	ctx context.Context,
	reader io.Reader,
	writer io.Writer,
	originalRequest map[string]interface{},
	upstreamURL string,
	headers http.Header,
) error {
	ch.mutex.RLock()
	initialized := ch.initialized
	ch.mutex.RUnlock()

	// 如果未初始化，使用简单流处理
	if !initialized || ch.streamProcessor == nil {
		ch.logger.Warn("Gemini enhanced features not available, using simple stream processing")
		return ch.processSimpleStream(ctx, reader, writer)
	}

	// 使用增强的流处理器
	return ch.streamProcessor.ProcessStreamWithRetry(
		ctx,
		reader,
		writer,
		originalRequest,
		upstreamURL,
		headers,
	)
}

// processSimpleStream 简单的流处理（回退模式）
func (ch *GeminiChannel) processSimpleStream(ctx context.Context, reader io.Reader, writer io.Writer) error {
	if ch.streamProcessor != nil {
		return ch.streamProcessor.ProcessSimpleStream(ctx, reader, writer)
	}

	// 最基础的流复制
	_, err := io.Copy(writer, reader)
	return err
}

// GetGeminiStats 获取 Gemini 处理统计
func (ch *GeminiChannel) GetGeminiStats() *gemini.StreamStats {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if !ch.initialized || ch.streamProcessor == nil {
		return &gemini.StreamStats{}
	}

	return ch.streamProcessor.GetStats()
}

// GetGeminiDetailedStats 获取详细的 Gemini 统计
func (ch *GeminiChannel) GetGeminiDetailedStats() *gemini.DetailedStats {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if !ch.initialized || ch.streamProcessor == nil {
		return &gemini.DetailedStats{}
	}

	return ch.streamProcessor.GetDetailedStats()
}

// GetGeminiHealthStatus 获取 Gemini 健康状态
func (ch *GeminiChannel) GetGeminiHealthStatus() *gemini.HealthStatus {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if !ch.initialized || ch.streamProcessor == nil {
		return &gemini.HealthStatus{
			Status: "disabled",
		}
	}

	return ch.streamProcessor.GetHealthStatus()
}

// UpdateGeminiConfig 更新 Gemini 配置
func (ch *GeminiChannel) UpdateGeminiConfig(update *gemini.ConfigUpdate) error {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()

	if !ch.initialized || ch.configManager == nil {
		return fmt.Errorf("Gemini components not initialized")
	}

	// 更新配置
	if err := ch.configManager.UpdateConfig(update); err != nil {
		return fmt.Errorf("failed to update Gemini config: %w", err)
	}

	// 更新流处理器配置
	if ch.streamProcessor != nil {
		config := ch.configManager.GetConfig()
		if err := ch.streamProcessor.UpdateConfig(config); err != nil {
			return fmt.Errorf("failed to update stream processor config: %w", err)
		}
	}

	ch.logger.Info("Gemini configuration updated successfully")
	return nil
}

// ResetGeminiStats 重置 Gemini 统计
func (ch *GeminiChannel) ResetGeminiStats() error {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if !ch.initialized || ch.streamProcessor == nil {
		return fmt.Errorf("Gemini components not initialized")
	}

	ch.streamProcessor.ResetStats()
	ch.logger.Info("Gemini statistics reset")
	return nil
}

// IsGeminiEnhancedEnabled 检查 Gemini 增强功能是否启用
func (ch *GeminiChannel) IsGeminiEnhancedEnabled() bool {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	return ch.initialized && ch.streamProcessor != nil
}

// GetGeminiConfig 获取当前 Gemini 配置
func (ch *GeminiChannel) GetGeminiConfig() map[string]interface{} {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if !ch.initialized || ch.configManager == nil {
		return map[string]interface{}{
			"enabled": false,
			"error":   "Gemini components not initialized",
		}
	}

	config := ch.configManager.GetConfigAsMap()
	config["enabled"] = true
	return config
}

// LogGeminiStats 记录 Gemini 统计信息
func (ch *GeminiChannel) LogGeminiStats() {
	ch.mutex.RLock()
	defer ch.mutex.RUnlock()

	if ch.initialized && ch.streamProcessor != nil {
		ch.streamProcessor.LogStatsIfSignificant()
	}
}
