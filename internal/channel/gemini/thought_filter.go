package gemini

import (
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// ThoughtFilter handles filtering of Gemini's thinking content
type ThoughtFilter struct {
	config *GeminiConfig
	logger *logrus.Logger
	
	// 过滤状态
	mutex             sync.RWMutex
	swallowModeActive bool
	
	// 状态跟踪
	processingState *ProcessingState
}

// NewThoughtFilter creates a new thought filter
func NewThoughtFilter(config *GeminiConfig, logger *logrus.Logger) *ThoughtFilter {
	return &ThoughtFilter{
		config: config,
		logger: logger,
		processingState: &ProcessingState{
			IsOutputtingFormalText: false,
			SwallowModeActive:     false,
			ResumePunctStreak:     0,
			LastFormalText:        "",
			LastFormalTextFlushed: false,
		},
	}
}

// ShouldSwallowThought determines if a thought should be filtered out
func (tf *ThoughtFilter) ShouldSwallowThought(isThought bool, isRetry bool) bool {
	tf.mutex.RLock()
	defer tf.mutex.RUnlock()
	
	// 如果不是重试，不过滤任何内容
	if !isRetry {
		return false
	}
	
	// 如果配置禁用了思考过滤，不过滤
	if !tf.config.SwallowThoughtsAfterRetry {
		return false
	}
	
	// 如果当前不在吞噬模式，不过滤
	if !tf.swallowModeActive {
		return false
	}
	
	// 如果是思考内容，过滤掉
	if isThought {
		if tf.config.EnableDetailedLogging {
			tf.logger.Debug("Filtering thought content")
		}
		return true
	}
	
	// 如果是正式文本，检查是否应该恢复正常输出
	if !isThought {
		tf.processingState.IsOutputtingFormalText = true
		if tf.config.EnableDetailedLogging {
			tf.logger.Debug("Detected formal text, considering disabling swallow mode")
		}
	}
	
	return false
}

// EnableSwallowMode enables thought filtering mode
func (tf *ThoughtFilter) EnableSwallowMode() {
	tf.mutex.Lock()
	defer tf.mutex.Unlock()
	
	tf.swallowModeActive = true
	tf.processingState.SwallowModeActive = true
	tf.processingState.IsOutputtingFormalText = false
	tf.processingState.ResumePunctStreak = 0
	tf.processingState.LastFormalText = ""
	tf.processingState.LastFormalTextFlushed = false
	
	if tf.config.EnableDetailedLogging {
		tf.logger.Debug("Enabled swallow mode for thought filtering")
	}
}

// DisableSwallowMode disables thought filtering mode
func (tf *ThoughtFilter) DisableSwallowMode() {
	tf.mutex.Lock()
	defer tf.mutex.Unlock()
	
	tf.swallowModeActive = false
	tf.processingState.SwallowModeActive = false
	
	if tf.config.EnableDetailedLogging {
		tf.logger.Debug("Disabled swallow mode, resuming normal output")
	}
}

// IsSwallowModeActive returns whether swallow mode is currently active
func (tf *ThoughtFilter) IsSwallowModeActive() bool {
	tf.mutex.RLock()
	defer tf.mutex.RUnlock()
	
	return tf.swallowModeActive
}

// ProcessTextChunk processes a text chunk and determines filtering behavior
func (tf *ThoughtFilter) ProcessTextChunk(text string, isThought bool, isRetry bool) (shouldOutput bool, filteredText string) {
	tf.mutex.Lock()
	defer tf.mutex.Unlock()
	
	// 如果不是重试或未启用过滤，直接输出
	if !isRetry || !tf.config.SwallowThoughtsAfterRetry {
		return true, text
	}
	
	// 如果不在吞噬模式，直接输出
	if !tf.swallowModeActive {
		return true, text
	}
	
	// 如果是思考内容，过滤掉
	if isThought {
		if tf.config.EnableDetailedLogging {
			tf.logger.Debugf("Filtering thought chunk: %s", tf.truncateForLog(text))
		}
		return false, ""
	}
	
	// 处理正式文本
	return tf.processFormalText(text)
}

// processFormalText processes formal (non-thought) text content
func (tf *ThoughtFilter) processFormalText(text string) (shouldOutput bool, filteredText string) {
	// 更新状态
	tf.processingState.IsOutputtingFormalText = true
	tf.processingState.LastFormalText = text
	
	// 如果启用了标点启发式，检查是否应该恢复正常输出
	if tf.config.EnablePunctuationHeuristic {
		if tf.shouldResumeBasedOnPunctuation(text) {
			tf.swallowModeActive = false
			tf.processingState.SwallowModeActive = false
			
			if tf.config.EnableDetailedLogging {
				tf.logger.Debug("Resuming normal output based on punctuation heuristic")
			}
		}
	} else {
		// 如果没有启用标点启发式，检测到正式文本就立即恢复
		tf.swallowModeActive = false
		tf.processingState.SwallowModeActive = false
		
		if tf.config.EnableDetailedLogging {
			tf.logger.Debug("Resuming normal output after detecting formal text")
		}
	}
	
	return true, text
}

// shouldResumeBasedOnPunctuation determines if output should resume based on punctuation
func (tf *ThoughtFilter) shouldResumeBasedOnPunctuation(text string) bool {
	// 检查文本是否以句子结束标点符号结尾
	trimmedText := strings.TrimSpace(text)
	if len(trimmedText) == 0 {
		return false
	}
	
	lastChar := trimmedText[len(trimmedText)-1]
	sentencePunctuation := []byte{'.', '!', '?', '。', '！', '？'}
	
	for _, punct := range sentencePunctuation {
		if lastChar == punct {
			tf.processingState.ResumePunctStreak++
			
			// 如果连续检测到句子结束标点，认为应该恢复正常输出
			if tf.processingState.ResumePunctStreak >= 2 {
				return true
			}
			
			return false
		}
	}
	
	// 重置标点连续计数
	tf.processingState.ResumePunctStreak = 0
	return false
}

// GetProcessingState returns the current processing state
func (tf *ThoughtFilter) GetProcessingState() *ProcessingState {
	tf.mutex.RLock()
	defer tf.mutex.RUnlock()
	
	// 返回状态的副本
	return &ProcessingState{
		IsOutputtingFormalText: tf.processingState.IsOutputtingFormalText,
		SwallowModeActive:     tf.processingState.SwallowModeActive,
		ResumePunctStreak:     tf.processingState.ResumePunctStreak,
		LastFormalText:        tf.processingState.LastFormalText,
		LastFormalTextFlushed: tf.processingState.LastFormalTextFlushed,
	}
}

// Reset resets the filter state
func (tf *ThoughtFilter) Reset() {
	tf.mutex.Lock()
	defer tf.mutex.Unlock()
	
	tf.swallowModeActive = false
	tf.processingState = &ProcessingState{
		IsOutputtingFormalText: false,
		SwallowModeActive:     false,
		ResumePunctStreak:     0,
		LastFormalText:        "",
		LastFormalTextFlushed: false,
	}
	
	if tf.config.EnableDetailedLogging {
		tf.logger.Debug("Reset thought filter state")
	}
}

// UpdateConfig updates the filter configuration
func (tf *ThoughtFilter) UpdateConfig(config *GeminiConfig) {
	tf.mutex.Lock()
	defer tf.mutex.Unlock()
	
	tf.config = config
	
	if tf.config.EnableDetailedLogging {
		tf.logger.Debug("Updated thought filter configuration")
	}
}

// GetFilterStats returns filtering statistics
func (tf *ThoughtFilter) GetFilterStats() map[string]interface{} {
	tf.mutex.RLock()
	defer tf.mutex.RUnlock()
	
	return map[string]interface{}{
		"swallow_mode_active":        tf.swallowModeActive,
		"is_outputting_formal_text":  tf.processingState.IsOutputtingFormalText,
		"resume_punct_streak":        tf.processingState.ResumePunctStreak,
		"last_formal_text_flushed":   tf.processingState.LastFormalTextFlushed,
		"swallow_thoughts_enabled":   tf.config.SwallowThoughtsAfterRetry,
		"punctuation_heuristic_enabled": tf.config.EnablePunctuationHeuristic,
	}
}

// truncateForLog truncates text for logging purposes
func (tf *ThoughtFilter) truncateForLog(text string) string {
	const maxLogLength = 100
	if len(text) <= maxLogLength {
		return text
	}
	return text[:maxLogLength] + "..."
}

// IsThoughtContent attempts to detect if content is thinking-related
func (tf *ThoughtFilter) IsThoughtContent(text string) bool {
	// 简单的思考内容检测启发式
	thinkingIndicators := []string{
		"let me think",
		"i think",
		"i need to",
		"let me consider",
		"thinking about",
		"i should",
		"let me analyze",
		"considering",
	}
	
	lowerText := strings.ToLower(text)
	for _, indicator := range thinkingIndicators {
		if strings.Contains(lowerText, indicator) {
			return true
		}
	}
	
	return false
}

// ShouldFilterBasedOnContent determines if content should be filtered based on its text
func (tf *ThoughtFilter) ShouldFilterBasedOnContent(text string, isRetry bool) bool {
	if !isRetry || !tf.config.SwallowThoughtsAfterRetry {
		return false
	}
	
	if !tf.IsSwallowModeActive() {
		return false
	}
	
	// 如果内容看起来像思考过程，过滤掉
	return tf.IsThoughtContent(text)
}
