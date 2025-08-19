package errors

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IsRateLimitError 检查是否为429错误
func IsRateLimitError(statusCode int, errorMessage string) bool {
	// 检查HTTP状态码
	if statusCode == http.StatusTooManyRequests {
		return true
	}

	// 检查错误消息中的关键词
	errorLower := strings.ToLower(errorMessage)
	rateLimitKeywords := []string{
		// 通用关键词
		"rate limit",
		"rate_limit",
		"rate-limit",
		"ratelimit",
		"too many requests",
		"quota exceeded",
		"quota_exceeded",
		"quota-exceeded",
		"quotaexceeded",

		// 时间相关限制
		"requests per minute",
		"requests per day",
		"requests per hour",
		"requests per second",
		"rpm exceeded",
		"rph exceeded",
		"rpd exceeded",
		"rps exceeded",
		"daily limit",
		"monthly limit",
		"hourly limit",
		"usage limit",
		"api limit",

		// 状态描述
		"throttled",
		"throttling",
		"rate limiting",
		"rate-limiting",
		"ratelimiting",
		"limited",
		"exceeded",
		"overload",
		"busy",

		// 特定服务商关键词
		"openai rate limit",
		"anthropic rate limit",
		"google quota",
		"azure rate limit",
		"aws throttling",
		"cloudflare rate limit",

		// 错误代码
		"error_code_429",
		"error_429",
		"http_429",
		"status_429",

		// 中文关键词
		"请求过于频繁",
		"访问频率限制",
		"配额已用完",
		"请求次数超限",
		"流量限制",
		"频率限制",
	}

	for _, keyword := range rateLimitKeywords {
		if strings.Contains(errorLower, keyword) {
			return true
		}
	}

	// 检查JSON格式的错误消息
	if isJSONRateLimitError(errorMessage) {
		return true
	}

	return false
}

// isJSONRateLimitError 检查JSON格式的错误消息是否包含429相关信息
func isJSONRateLimitError(errorMessage string) bool {
	// 简单的JSON关键词检查
	jsonKeywords := []string{
		`"error_code":"429"`,
		`"error_code":429`,
		`"status_code":"429"`,
		`"status_code":429`,
		`"error":"rate_limit"`,
		`"error":"rate limit"`,
		`"error":"too_many_requests"`,
		`"error":"quota_exceeded"`,
		`"type":"rate_limit_error"`,
		`"code":"rate_limit"`,
		`"code":"429"`,
		`"message":"rate limit"`,
		`"message":"too many requests"`,
		`"message":"quota exceeded"`,
	}

	errorLower := strings.ToLower(errorMessage)
	for _, keyword := range jsonKeywords {
		if strings.Contains(errorLower, keyword) {
			return true
		}
	}

	return false
}

// ParseRetryAfter 解析Retry-After头或错误消息中的重试时间
func ParseRetryAfter(retryAfterHeader string, errorMessage string) time.Duration {
	// 默认重试时间
	defaultRetryAfter := 60 * time.Second

	// 首先尝试解析Retry-After头
	if retryAfterHeader != "" {
		if duration := parseRetryAfterHeader(retryAfterHeader); duration > 0 {
			return duration
		}
	}

	// 如果没有Retry-After头，尝试从错误消息中解析
	if duration := parseRetryAfterFromMessage(errorMessage); duration > 0 {
		return duration
	}

	return defaultRetryAfter
}

// parseRetryAfterHeader 解析Retry-After头
func parseRetryAfterHeader(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}

	// 尝试解析为秒数
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	// 尝试解析为HTTP日期格式
	if t, err := http.ParseTime(header); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return duration
		}
	}

	return 0
}

// parseRetryAfterFromMessage 从错误消息中解析重试时间
func parseRetryAfterFromMessage(message string) time.Duration {
	if message == "" {
		return 0
	}

	messageLower := strings.ToLower(message)

	// 常见的重试时间模式
	patterns := []struct {
		keywords []string
		duration time.Duration
	}{
		// 秒级别
		{[]string{"try again in 30 seconds", "wait 30 seconds", "30 seconds", "30s"}, 30 * time.Second},
		{[]string{"try again in 60 seconds", "wait 60 seconds", "60 seconds", "60s"}, 60 * time.Second},

		// 分钟级别
		{[]string{"try again in 1 minute", "wait 1 minute", "1 minute", "1m"}, 1 * time.Minute},
		{[]string{"try again in 2 minutes", "wait 2 minutes", "2 minutes", "2m"}, 2 * time.Minute},
		{[]string{"try again in 3 minutes", "wait 3 minutes", "3 minutes", "3m"}, 3 * time.Minute},
		{[]string{"try again in 5 minutes", "wait 5 minutes", "5 minutes", "5m"}, 5 * time.Minute},
		{[]string{"try again in 10 minutes", "wait 10 minutes", "10 minutes", "10m"}, 10 * time.Minute},
		{[]string{"try again in 15 minutes", "wait 15 minutes", "15 minutes", "15m"}, 15 * time.Minute},
		{[]string{"try again in 20 minutes", "wait 20 minutes", "20 minutes", "20m"}, 20 * time.Minute},
		{[]string{"try again in 30 minutes", "wait 30 minutes", "30 minutes", "30m"}, 30 * time.Minute},

		// 小时级别
		{[]string{"try again in 1 hour", "wait 1 hour", "1 hour", "1h"}, 1 * time.Hour},
		{[]string{"try again in 2 hours", "wait 2 hours", "2 hours", "2h"}, 2 * time.Hour},
		{[]string{"try again in 3 hours", "wait 3 hours", "3 hours", "3h"}, 3 * time.Hour},
		{[]string{"try again in 6 hours", "wait 6 hours", "6 hours", "6h"}, 6 * time.Hour},
		{[]string{"try again in 12 hours", "wait 12 hours", "12 hours", "12h"}, 12 * time.Hour},

		// 天级别
		{[]string{"daily limit", "24 hours", "tomorrow", "next day", "1 day", "24h"}, 24 * time.Hour},
		{[]string{"try again tomorrow", "wait until tomorrow", "reset tomorrow"}, 24 * time.Hour},

		// 月级别
		{[]string{"monthly limit", "next month", "30 days"}, 30 * 24 * time.Hour},

		// 特定服务商模式
		{[]string{"openai rate limit", "please try again later"}, 1 * time.Minute},
		{[]string{"anthropic rate limit"}, 1 * time.Minute},
		{[]string{"google quota exceeded"}, 1 * time.Hour},
		{[]string{"azure throttling"}, 1 * time.Minute},

		// 中文模式
		{[]string{"请稍后重试", "请等待1分钟", "1分钟后重试"}, 1 * time.Minute},
		{[]string{"请等待5分钟", "5分钟后重试"}, 5 * time.Minute},
		{[]string{"请等待1小时", "1小时后重试"}, 1 * time.Hour},
		{[]string{"明天重试", "24小时后重试"}, 24 * time.Hour},
	}

	for _, pattern := range patterns {
		for _, keyword := range pattern.keywords {
			if strings.Contains(messageLower, keyword) {
				return pattern.duration
			}
		}
	}

	// 使用正则表达式解析数字+时间单位的模式
	if duration := parseTimeFromMessage(messageLower); duration > 0 {
		return duration
	}

	return 0
}

// parseTimeFromMessage 使用正则表达式从错误消息中解析时间
func parseTimeFromMessage(message string) time.Duration {
	// 定义时间解析的正则表达式模式
	patterns := []struct {
		regex    *regexp.Regexp
		unit     time.Duration
		multiplier int
	}{
		// 秒
		{regexp.MustCompile(`(\d+)\s*(?:seconds?|secs?|s)\b`), time.Second, 1},
		// 分钟
		{regexp.MustCompile(`(\d+)\s*(?:minutes?|mins?|m)\b`), time.Minute, 1},
		// 小时
		{regexp.MustCompile(`(\d+)\s*(?:hours?|hrs?|h)\b`), time.Hour, 1},
		// 天
		{regexp.MustCompile(`(\d+)\s*(?:days?|d)\b`), time.Hour, 24},
		// 周
		{regexp.MustCompile(`(\d+)\s*(?:weeks?|w)\b`), time.Hour, 24 * 7},

		// 特殊模式：wait X seconds/minutes/hours
		{regexp.MustCompile(`wait\s+(\d+)\s*(?:seconds?|secs?|s)\b`), time.Second, 1},
		{regexp.MustCompile(`wait\s+(\d+)\s*(?:minutes?|mins?|m)\b`), time.Minute, 1},
		{regexp.MustCompile(`wait\s+(\d+)\s*(?:hours?|hrs?|h)\b`), time.Hour, 1},

		// 特殊模式：try again in X seconds/minutes/hours
		{regexp.MustCompile(`try\s+again\s+in\s+(\d+)\s*(?:seconds?|secs?|s)\b`), time.Second, 1},
		{regexp.MustCompile(`try\s+again\s+in\s+(\d+)\s*(?:minutes?|mins?|m)\b`), time.Minute, 1},
		{regexp.MustCompile(`try\s+again\s+in\s+(\d+)\s*(?:hours?|hrs?|h)\b`), time.Hour, 1},

		// 特殊模式：retry after X seconds/minutes/hours
		{regexp.MustCompile(`retry\s+after\s+(\d+)\s*(?:seconds?|secs?|s)\b`), time.Second, 1},
		{regexp.MustCompile(`retry\s+after\s+(\d+)\s*(?:minutes?|mins?|m)\b`), time.Minute, 1},
		{regexp.MustCompile(`retry\s+after\s+(\d+)\s*(?:hours?|hrs?|h)\b`), time.Hour, 1},

		// 中文模式
		{regexp.MustCompile(`(\d+)\s*秒`), time.Second, 1},
		{regexp.MustCompile(`(\d+)\s*分钟`), time.Minute, 1},
		{regexp.MustCompile(`(\d+)\s*小时`), time.Hour, 1},
		{regexp.MustCompile(`(\d+)\s*天`), time.Hour, 24},
	}

	for _, pattern := range patterns {
		matches := pattern.regex.FindStringSubmatch(message)
		if len(matches) > 1 {
			if num, err := strconv.Atoi(matches[1]); err == nil {
				duration := time.Duration(num * pattern.multiplier) * pattern.unit
				// 限制最大重试时间为7天
				if duration > 7*24*time.Hour {
					duration = 7 * 24 * time.Hour
				}
				return duration
			}
		}
	}

	return 0
}

// CreateRateLimitError 创建429错误对象
func CreateRateLimitError(statusCode int, errorMessage string, retryAfterHeader string) *RateLimitError {
	retryAfter := ParseRetryAfter(retryAfterHeader, errorMessage)

	var resetAt *time.Time
	if retryAfter > 0 {
		t := time.Now().Add(retryAfter)
		resetAt = &t
	}

	return &RateLimitError{
		APIError: &APIError{
			HTTPStatus: http.StatusTooManyRequests,
			Code:       "RATE_LIMITED",
			Message:    errorMessage,
		},
		RetryAfter: retryAfter,
		ResetAt:    resetAt,
	}
}

// GetRetryAfterSeconds 获取重试等待秒数
func (e *RateLimitError) GetRetryAfterSeconds() int {
	return int(e.RetryAfter.Seconds())
}

// IsExpired 检查是否已过期（可以重试）
func (e *RateLimitError) IsExpired() bool {
	if e.ResetAt == nil {
		return false
	}
	return time.Now().After(*e.ResetAt)
}
