package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// RetryEngine handles intelligent stream retry logic with context preservation
type RetryEngine struct {
	config         *GeminiConfig
	sseParser      *SSEParser
	thoughtFilter  *ThoughtFilter
	statsCollector *StatsCollector
	logger         *logrus.Logger
}

// NewRetryEngine creates a new retry engine
func NewRetryEngine(config *GeminiConfig, sseParser *SSEParser, thoughtFilter *ThoughtFilter, statsCollector *StatsCollector, logger *logrus.Logger) *RetryEngine {
	return &RetryEngine{
		config:         config,
		sseParser:      sseParser,
		thoughtFilter:  thoughtFilter,
		statsCollector: statsCollector,
		logger:         logger,
	}
}

// ProcessStreamWithRetry processes a stream with intelligent retry logic
func (re *RetryEngine) ProcessStreamWithRetry(
	ctx context.Context,
	initialReader io.Reader,
	writer io.Writer,
	originalRequestBody map[string]interface{},
	upstreamURL string,
	originalHeaders http.Header,
) error {
	retryContext := &RetryContext{
		AccumulatedText:    "",
		RetryCount:         0,
		InterruptionReason: "",
		OriginalRequest:    originalRequestBody,
		StartTime:          time.Now(),
	}

	// 处理初始流
	err := re.processStream(ctx, initialReader, writer, retryContext, false)
	if err == nil {
		// 成功完成，记录统计
		duration := time.Since(retryContext.StartTime)
		re.statsCollector.RecordStreamSuccess(duration, retryContext.RetryCount)
		return nil
	}

	// 检查是否需要重试
	if !re.shouldRetry(err, retryContext) {
		duration := time.Since(retryContext.StartTime)
		re.statsCollector.RecordStreamInterruption(InterruptionReason(retryContext.InterruptionReason), duration, retryContext.RetryCount)
		return err
	}

	// 开始重试循环
	return re.retryLoop(ctx, writer, retryContext, upstreamURL, originalHeaders)
}

// processStream processes a single stream and detects interruptions
func (re *RetryEngine) processStream(ctx context.Context, reader io.Reader, writer io.Writer, retryContext *RetryContext, isRetry bool) error {
	scanner := bufio.NewScanner(reader)
	lineCount := 0
	lastFinishReason := ""
	hasContent := false

	// 如果是重试，启用思考过滤
	if isRetry && re.config.SwallowThoughtsAfterRetry {
		re.thoughtFilter.EnableSwallowMode()
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		lineCount++

		if re.config.EnableDetailedLogging {
			logLine := line
			if len(line) > 200 {
				logLine = line[:200] + "..."
			}
			re.logger.Debugf("Processing line %d: %s", lineCount, logLine)
		}

		// 检查是否为阻塞行
		if re.sseParser.IsBlockedLine(line) {
			retryContext.InterruptionReason = string(InterruptionBlock)
			return fmt.Errorf("content blocked detected")
		}

		// 解析行内容
		if re.sseParser.IsDataLine(line) {
			content := re.sseParser.ParseLineContent(line)
			if content.Text != "" {
				hasContent = true
				
				// 检查思考过滤
				if re.thoughtFilter.ShouldSwallowThought(content.IsThought, isRetry) {
					re.statsCollector.RecordThoughtFiltered()
					if re.config.EnableDetailedLogging {
						re.logger.Debug("Swallowing thought content")
					}
					continue
				}

				// 累积文本
				retryContext.AccumulatedText += content.Text

				// 检查输出字符限制
				if re.config.MaxOutputChars > 0 && len(retryContext.AccumulatedText) > re.config.MaxOutputChars {
					retryContext.InterruptionReason = string(InterruptionTimeout)
					return fmt.Errorf("output character limit exceeded")
				}

				// 如果是重试且检测到正式文本，恢复正常输出
				if isRetry && !content.IsThought {
					re.thoughtFilter.DisableSwallowMode()
				}
			}

			// 提取完成原因
			if finishReason := re.sseParser.ExtractFinishReason(line); finishReason != "" {
				lastFinishReason = finishReason
			}
		}

		// 写入输出（可能经过过滤）
		if _, err := writer.Write([]byte(line + "\n")); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		retryContext.InterruptionReason = string(InterruptionDrop)
		return fmt.Errorf("stream reading error: %w", err)
	}

	// 检查流是否正常完成
	if !re.sseParser.ValidateStreamCompletion(retryContext.AccumulatedText, lastFinishReason) {
		if lastFinishReason == "STOP" {
			retryContext.InterruptionReason = string(InterruptionIncomplete)
			return fmt.Errorf("stream ended without proper completion")
		} else if lastFinishReason != "" {
			retryContext.InterruptionReason = string(InterruptionFinishAbnormal)
			return fmt.Errorf("abnormal finish reason: %s", lastFinishReason)
		} else {
			retryContext.InterruptionReason = string(InterruptionDrop)
			return fmt.Errorf("stream dropped without finish reason")
		}
	}

	if !hasContent {
		retryContext.InterruptionReason = string(InterruptionDrop)
		return fmt.Errorf("no content received")
	}

	return nil
}

// shouldRetry determines if a retry should be attempted
func (re *RetryEngine) shouldRetry(err error, retryContext *RetryContext) bool {
	if retryContext.RetryCount >= re.config.MaxConsecutiveRetries {
		re.logger.Warnf("Maximum retry count reached: %d", re.config.MaxConsecutiveRetries)
		return false
	}

	// 检查是否为可重试的错误
	retryableReasons := []InterruptionReason{
		InterruptionBlock,
		InterruptionDrop,
		InterruptionIncomplete,
		InterruptionFinishAbnormal,
	}

	currentReason := InterruptionReason(retryContext.InterruptionReason)
	for _, reason := range retryableReasons {
		if currentReason == reason {
			return true
		}
	}

	return false
}

// retryLoop executes the retry logic
func (re *RetryEngine) retryLoop(ctx context.Context, writer io.Writer, retryContext *RetryContext, upstreamURL string, originalHeaders http.Header) error {
	for retryContext.RetryCount < re.config.MaxConsecutiveRetries {
		retryContext.RetryCount++
		
		re.logger.Infof("Attempting retry %d/%d for reason: %s", 
			retryContext.RetryCount, re.config.MaxConsecutiveRetries, retryContext.InterruptionReason)

		// 等待重试延迟
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(re.config.RetryDelayMs):
		}

		// 构建重试请求
		retryBody, err := re.buildRetryRequestBody(retryContext)
		if err != nil {
			re.logger.Errorf("Failed to build retry request: %v", err)
			continue
		}

		// 执行重试请求
		retryStartTime := time.Now()
		resp, err := re.executeRetryRequest(ctx, upstreamURL, retryBody, originalHeaders)
		if err != nil {
			re.logger.Errorf("Retry request failed: %v", err)
			continue
		}

		// 处理重试响应
		err = re.processStream(ctx, resp.Body, writer, retryContext, true)
		resp.Body.Close()
		
		retryDuration := time.Since(retryStartTime)
		re.statsCollector.RecordRetryAttempt(retryDuration)

		if err == nil {
			// 重试成功
			totalDuration := time.Since(retryContext.StartTime)
			re.statsCollector.RecordStreamSuccess(totalDuration, retryContext.RetryCount)
			re.logger.Infof("Retry successful after %d attempts", retryContext.RetryCount)
			return nil
		}

		// 检查是否应该继续重试
		if !re.shouldRetry(err, retryContext) {
			break
		}
	}

	// 所有重试都失败了
	totalDuration := time.Since(retryContext.StartTime)
	re.statsCollector.RecordStreamInterruption(InterruptionReason(retryContext.InterruptionReason), totalDuration, retryContext.RetryCount)
	
	return fmt.Errorf("all retries failed after %d attempts, last error: %v", retryContext.RetryCount, retryContext.InterruptionReason)
}

// buildRetryRequestBody constructs a retry request with accumulated context
func (re *RetryEngine) buildRetryRequestBody(retryContext *RetryContext) ([]byte, error) {
	// 创建重试请求体
	retryRequest := RetryRequest{
		Contents: []Content{},
	}

	// 复制原始请求的配置
	if originalContents, ok := retryContext.OriginalRequest["contents"].([]interface{}); ok {
		for _, content := range originalContents {
			if contentMap, ok := content.(map[string]interface{}); ok {
				var parts []Part
				if partsArray, ok := contentMap["parts"].([]interface{}); ok {
					for _, part := range partsArray {
						if partMap, ok := part.(map[string]interface{}); ok {
							if text, ok := partMap["text"].(string); ok {
								parts = append(parts, Part{Text: text})
							}
						}
					}
				}
				
				role := "user"
				if r, ok := contentMap["role"].(string); ok {
					role = r
				}
				
				retryRequest.Contents = append(retryRequest.Contents, Content{
					Role:  role,
					Parts: parts,
				})
			}
		}
	}

	// 添加已累积的文本作为上下文
	if retryContext.AccumulatedText != "" {
		contextContent := Content{
			Role: "model",
			Parts: []Part{
				{Text: retryContext.AccumulatedText},
			},
		}
		retryRequest.Contents = append(retryRequest.Contents, contextContent)

		// 添加续写提示
		continueContent := Content{
			Role: "user",
			Parts: []Part{
				{Text: "Please continue from where you left off."},
			},
		}
		retryRequest.Contents = append(retryRequest.Contents, continueContent)
	}

	// 复制其他配置
	if genConfig, ok := retryContext.OriginalRequest["generationConfig"]; ok {
		retryRequest.GenerationConfig = genConfig.(GenerationConfig)
	}
	if safetySettings, ok := retryContext.OriginalRequest["safetySettings"]; ok {
		retryRequest.SafetySettings = safetySettings.([]SafetySetting)
	}
	if sysInstruction, ok := retryContext.OriginalRequest["systemInstruction"]; ok {
		retryRequest.SystemInstruction = sysInstruction.(*SystemInstruction)
	}

	return json.Marshal(retryRequest)
}

// executeRetryRequest executes a retry HTTP request
func (re *RetryEngine) executeRetryRequest(ctx context.Context, upstreamURL string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create retry request: %w", err)
	}

	// 复制原始头部
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// 设置内容类型
	req.Header.Set("Content-Type", "application/json")

	// 保存重试请求（如果启用）
	if re.config.SaveRetryRequests {
		re.logger.Debugf("Retry request body: %s", string(body))
	}

	client := &http.Client{
		Timeout: re.config.StreamTimeout,
	}

	return client.Do(req)
}
