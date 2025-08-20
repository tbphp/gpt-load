package gemini

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// StreamProcessor is the main coordinator for Gemini stream processing
type StreamProcessor struct {
	config         *GeminiConfig
	retryEngine    *RetryEngine
	thoughtFilter  *ThoughtFilter
	sseParser      *SSEParser
	statsCollector *StatsCollector
	logger         *logrus.Logger
}

// NewStreamProcessor creates a new stream processor with all components
func NewStreamProcessor(config *GeminiConfig, logger *logrus.Logger) *StreamProcessor {
	// 创建组件
	sseParser := NewSSEParser(logger, config)
	statsCollector := NewStatsCollector(logger)
	thoughtFilter := NewThoughtFilter(config, logger)
	retryEngine := NewRetryEngine(config, sseParser, thoughtFilter, statsCollector, logger)

	return &StreamProcessor{
		config:         config,
		retryEngine:    retryEngine,
		thoughtFilter:  thoughtFilter,
		sseParser:      sseParser,
		statsCollector: statsCollector,
		logger:         logger,
	}
}

// ProcessStreamWithRetry is the main entry point for processing streams with retry logic
func (sp *StreamProcessor) ProcessStreamWithRetry(
	ctx context.Context,
	initialReader io.Reader,
	writer io.Writer,
	originalRequest map[string]interface{},
	upstreamURL string,
	headers http.Header,
) error {
	// 记录流开始
	sp.statsCollector.RecordStreamStart()
	
	if sp.config.EnableDetailedLogging {
		sp.logger.Info("Starting Gemini stream processing with retry capability")
	}

	// 创建带超时的上下文
	ctx, cancel := context.WithTimeout(ctx, sp.config.StreamTimeout)
	defer cancel()

	// 重置过滤器状态
	sp.thoughtFilter.Reset()

	// 使用重试引擎处理流
	err := sp.retryEngine.ProcessStreamWithRetry(
		ctx,
		initialReader,
		writer,
		originalRequest,
		upstreamURL,
		headers,
	)

	if err != nil {
		sp.logger.Errorf("Stream processing failed: %v", err)
		return fmt.Errorf("gemini stream processing failed: %w", err)
	}

	if sp.config.EnableDetailedLogging {
		sp.logger.Info("Gemini stream processing completed successfully")
	}

	return nil
}

// ProcessSimpleStream processes a stream without retry logic (fallback mode)
func (sp *StreamProcessor) ProcessSimpleStream(
	ctx context.Context,
	reader io.Reader,
	writer io.Writer,
) error {
	sp.statsCollector.RecordStreamStart()
	
	if sp.config.EnableDetailedLogging {
		sp.logger.Info("Processing Gemini stream in simple mode (no retry)")
	}

	startTime := time.Now()
	
	// 直接复制流内容
	_, err := io.Copy(writer, reader)
	if err != nil {
		duration := time.Since(startTime)
		sp.statsCollector.RecordStreamInterruption(InterruptionDrop, duration, 0)
		return fmt.Errorf("simple stream processing failed: %w", err)
	}

	duration := time.Since(startTime)
	sp.statsCollector.RecordStreamSuccess(duration, 0)
	
	if sp.config.EnableDetailedLogging {
		sp.logger.Info("Simple stream processing completed")
	}

	return nil
}

// GetStats returns current processing statistics
func (sp *StreamProcessor) GetStats() *StreamStats {
	return sp.statsCollector.GetStats()
}

// GetDetailedStats returns detailed processing statistics
func (sp *StreamProcessor) GetDetailedStats() *DetailedStats {
	return sp.statsCollector.GetDetailedStats()
}

// GetHealthStatus returns the current health status
func (sp *StreamProcessor) GetHealthStatus() *HealthStatus {
	return sp.statsCollector.GetHealthStatus()
}

// ResetStats resets all statistics
func (sp *StreamProcessor) ResetStats() {
	sp.statsCollector.Reset()
	sp.thoughtFilter.Reset()
	
	sp.logger.Info("Reset Gemini stream processor statistics")
}

// UpdateConfig updates the processor configuration
func (sp *StreamProcessor) UpdateConfig(config *GeminiConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}

	sp.config = config
	
	// 更新各组件的配置
	sp.thoughtFilter.UpdateConfig(config)
	sp.retryEngine.config = config
	sp.sseParser.config = config

	if sp.config.EnableDetailedLogging {
		sp.logger.Info("Updated Gemini stream processor configuration")
	}

	return nil
}

// GetProcessingState returns the current processing state
func (sp *StreamProcessor) GetProcessingState() map[string]interface{} {
	state := map[string]interface{}{
		"stats":          sp.GetStats(),
		"health":         sp.GetHealthStatus(),
		"filter_stats":   sp.thoughtFilter.GetFilterStats(),
		"config":         sp.getConfigSummary(),
	}

	return state
}

// getConfigSummary returns a summary of current configuration
func (sp *StreamProcessor) getConfigSummary() map[string]interface{} {
	return map[string]interface{}{
		"max_retries":                    sp.config.MaxConsecutiveRetries,
		"retry_delay_ms":                int(sp.config.RetryDelayMs / time.Millisecond),
		"swallow_thoughts_after_retry":   sp.config.SwallowThoughtsAfterRetry,
		"enable_punctuation_heuristic":  sp.config.EnablePunctuationHeuristic,
		"enable_detailed_logging":       sp.config.EnableDetailedLogging,
		"save_retry_requests":           sp.config.SaveRetryRequests,
		"max_output_chars":              sp.config.MaxOutputChars,
		"stream_timeout":                int(sp.config.StreamTimeout / time.Second),
	}
}

// LogStats logs current statistics
func (sp *StreamProcessor) LogStats() {
	sp.statsCollector.LogStats()
}

// LogStatsIfSignificant logs statistics only if there's significant activity
func (sp *StreamProcessor) LogStatsIfSignificant() {
	sp.statsCollector.LogStatsIfSignificant()
}

// IsRetryCapable returns whether retry functionality is enabled and working
func (sp *StreamProcessor) IsRetryCapable() bool {
	return sp.config.MaxConsecutiveRetries > 0 && sp.retryEngine != nil
}

// GetComponentStatus returns the status of all components
func (sp *StreamProcessor) GetComponentStatus() map[string]interface{} {
	return map[string]interface{}{
		"retry_engine": map[string]interface{}{
			"enabled":     sp.retryEngine != nil,
			"max_retries": sp.config.MaxConsecutiveRetries,
		},
		"thought_filter": map[string]interface{}{
			"enabled":            sp.config.SwallowThoughtsAfterRetry,
			"swallow_mode_active": sp.thoughtFilter.IsSwallowModeActive(),
		},
		"sse_parser": map[string]interface{}{
			"enabled": sp.sseParser != nil,
		},
		"stats_collector": map[string]interface{}{
			"enabled":       sp.statsCollector != nil,
			"total_streams": sp.statsCollector.GetStats().TotalStreams,
		},
	}
}

// ValidateConfiguration validates the current configuration
func (sp *StreamProcessor) ValidateConfiguration() error {
	if sp.config.MaxConsecutiveRetries < 0 {
		return fmt.Errorf("max_consecutive_retries must be non-negative")
	}
	
	if sp.config.MaxConsecutiveRetries > 200 {
		return fmt.Errorf("max_consecutive_retries cannot exceed 200")
	}
	
	if sp.config.RetryDelayMs < 100*time.Millisecond {
		return fmt.Errorf("retry_delay_ms must be at least 100ms")
	}
	
	if sp.config.RetryDelayMs > 10*time.Second {
		return fmt.Errorf("retry_delay_ms cannot exceed 10 seconds")
	}
	
	if sp.config.StreamTimeout < 30*time.Second {
		return fmt.Errorf("stream_timeout must be at least 30 seconds")
	}
	
	if sp.config.StreamTimeout > 3600*time.Second {
		return fmt.Errorf("stream_timeout cannot exceed 1 hour")
	}
	
	if sp.config.MaxOutputChars < 0 {
		return fmt.Errorf("max_output_chars must be non-negative")
	}

	return nil
}

// Shutdown gracefully shuts down the stream processor
func (sp *StreamProcessor) Shutdown(ctx context.Context) error {
	sp.logger.Info("Shutting down Gemini stream processor")
	
	// 记录最终统计
	sp.LogStats()
	
	// 这里可以添加其他清理逻辑
	// 例如：等待正在进行的流处理完成、保存状态等
	
	select {
	case <-ctx.Done():
		sp.logger.Warn("Shutdown timeout reached")
		return ctx.Err()
	default:
		sp.logger.Info("Gemini stream processor shutdown completed")
		return nil
	}
}

// GetVersion returns the processor version information
func (sp *StreamProcessor) GetVersion() map[string]string {
	return map[string]string{
		"processor_version": "1.0.0",
		"features": "retry,thought_filter,sse_parser,stats",
		"build_time": time.Now().Format("2006-01-02 15:04:05"),
	}
}
