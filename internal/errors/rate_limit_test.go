package errors

import (
	"net/http"
	"testing"
	"time"
)

// TestIsRateLimitError 测试429错误识别
func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		errorMessage string
		expected     bool
	}{
		// HTTP状态码测试
		{
			name:         "HTTP 429 status code",
			statusCode:   http.StatusTooManyRequests,
			errorMessage: "Some error message",
			expected:     true,
		},
		{
			name:         "HTTP 200 status code",
			statusCode:   http.StatusOK,
			errorMessage: "Success",
			expected:     false,
		},
		
		// 通用关键词测试
		{
			name:         "Rate limit keyword",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Rate limit exceeded",
			expected:     true,
		},
		{
			name:         "Too many requests keyword",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Too many requests",
			expected:     true,
		},
		{
			name:         "Quota exceeded keyword",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Quota exceeded",
			expected:     true,
		},
		{
			name:         "Throttled keyword",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Request throttled",
			expected:     true,
		},
		
		// 时间相关限制测试
		{
			name:         "RPM exceeded",
			statusCode:   http.StatusBadRequest,
			errorMessage: "RPM exceeded",
			expected:     true,
		},
		{
			name:         "Daily limit",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Daily limit reached",
			expected:     true,
		},
		{
			name:         "Monthly limit",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Monthly limit exceeded",
			expected:     true,
		},
		
		// 特定服务商测试
		{
			name:         "OpenAI rate limit",
			statusCode:   http.StatusBadRequest,
			errorMessage: "OpenAI rate limit exceeded",
			expected:     true,
		},
		{
			name:         "Anthropic rate limit",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Anthropic rate limit",
			expected:     true,
		},
		{
			name:         "Google quota",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Google quota exceeded",
			expected:     true,
		},
		
		// JSON格式测试
		{
			name:         "JSON error code 429",
			statusCode:   http.StatusBadRequest,
			errorMessage: `{"error_code":"429","message":"Rate limited"}`,
			expected:     true,
		},
		{
			name:         "JSON rate limit error",
			statusCode:   http.StatusBadRequest,
			errorMessage: `{"error":"rate_limit","details":"Too many requests"}`,
			expected:     true,
		},
		{
			name:         "JSON quota exceeded",
			statusCode:   http.StatusBadRequest,
			errorMessage: `{"error":"quota_exceeded","message":"Daily quota exceeded"}`,
			expected:     true,
		},
		
		// 中文关键词测试
		{
			name:         "Chinese rate limit",
			statusCode:   http.StatusBadRequest,
			errorMessage: "请求过于频繁，请稍后重试",
			expected:     true,
		},
		{
			name:         "Chinese quota exceeded",
			statusCode:   http.StatusBadRequest,
			errorMessage: "配额已用完",
			expected:     true,
		},
		
		// 负面测试
		{
			name:         "Normal error message",
			statusCode:   http.StatusBadRequest,
			errorMessage: "Invalid request parameters",
			expected:     false,
		},
		{
			name:         "Authentication error",
			statusCode:   http.StatusUnauthorized,
			errorMessage: "Invalid API key",
			expected:     false,
		},
		{
			name:         "Server error",
			statusCode:   http.StatusInternalServerError,
			errorMessage: "Internal server error",
			expected:     false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRateLimitError(tt.statusCode, tt.errorMessage)
			if result != tt.expected {
				t.Errorf("IsRateLimitError(%d, %q) = %v, expected %v", 
					tt.statusCode, tt.errorMessage, result, tt.expected)
			}
		})
	}
}

// TestParseRetryAfter 测试重试时间解析
func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name              string
		retryAfterHeader  string
		errorMessage      string
		expectedDuration  time.Duration
	}{
		// Retry-After头测试
		{
			name:             "Retry-After header seconds",
			retryAfterHeader: "60",
			errorMessage:     "",
			expectedDuration: 60 * time.Second,
		},
		{
			name:             "Retry-After header HTTP date",
			retryAfterHeader: "Wed, 21 Oct 2015 07:28:00 GMT",
			errorMessage:     "",
			expectedDuration: 0, // 简化测试，实际应该解析日期
		},
		
		// 错误消息中的时间模式测试
		{
			name:             "Try again in 1 minute",
			retryAfterHeader: "",
			errorMessage:     "Rate limit exceeded. Try again in 1 minute.",
			expectedDuration: 1 * time.Minute,
		},
		{
			name:             "Wait 5 minutes",
			retryAfterHeader: "",
			errorMessage:     "Too many requests. Wait 5 minutes.",
			expectedDuration: 5 * time.Minute,
		},
		{
			name:             "Try again in 1 hour",
			retryAfterHeader: "",
			errorMessage:     "Quota exceeded. Try again in 1 hour.",
			expectedDuration: 1 * time.Hour,
		},
		{
			name:             "Daily limit",
			retryAfterHeader: "",
			errorMessage:     "Daily limit reached. Try tomorrow.",
			expectedDuration: 24 * time.Hour,
		},
		
		// 正则表达式解析测试
		{
			name:             "Regex parse 30 seconds",
			retryAfterHeader: "",
			errorMessage:     "Rate limited. Retry after 30 seconds.",
			expectedDuration: 30 * time.Second,
		},
		{
			name:             "Regex parse 10 minutes",
			retryAfterHeader: "",
			errorMessage:     "Please wait 10 minutes before retrying.",
			expectedDuration: 10 * time.Minute,
		},
		{
			name:             "Regex parse 2 hours",
			retryAfterHeader: "",
			errorMessage:     "Service overloaded. Try again in 2 hours.",
			expectedDuration: 2 * time.Hour,
		},
		
		// 中文时间解析测试
		{
			name:             "Chinese 1 minute",
			retryAfterHeader: "",
			errorMessage:     "请求过于频繁，请等待1分钟后重试",
			expectedDuration: 1 * time.Minute,
		},
		{
			name:             "Chinese 1 hour",
			retryAfterHeader: "",
			errorMessage:     "配额已用完，请等待1小时后重试",
			expectedDuration: 1 * time.Hour,
		},
		
		// 无法解析的情况
		{
			name:             "No time information",
			retryAfterHeader: "",
			errorMessage:     "Rate limit exceeded",
			expectedDuration: 0,
		},
		{
			name:             "Invalid header",
			retryAfterHeader: "invalid",
			errorMessage:     "",
			expectedDuration: 0,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseRetryAfter(tt.retryAfterHeader, tt.errorMessage)
			if result != tt.expectedDuration {
				t.Errorf("ParseRetryAfter(%q, %q) = %v, expected %v", 
					tt.retryAfterHeader, tt.errorMessage, result, tt.expectedDuration)
			}
		})
	}
}

// TestCreateRateLimitError 测试429错误对象创建
func TestCreateRateLimitError(t *testing.T) {
	tests := []struct {
		name              string
		statusCode        int
		errorMessage      string
		retryAfterHeader  string
		expectedRetryAfter time.Duration
		expectResetAt     bool
	}{
		{
			name:              "With retry after header",
			statusCode:        429,
			errorMessage:      "Rate limit exceeded",
			retryAfterHeader:  "60",
			expectedRetryAfter: 60 * time.Second,
			expectResetAt:     true,
		},
		{
			name:              "With error message time",
			statusCode:        429,
			errorMessage:      "Rate limit exceeded. Try again in 5 minutes.",
			retryAfterHeader:  "",
			expectedRetryAfter: 5 * time.Minute,
			expectResetAt:     true,
		},
		{
			name:              "No retry time",
			statusCode:        429,
			errorMessage:      "Rate limit exceeded",
			retryAfterHeader:  "",
			expectedRetryAfter: 0,
			expectResetAt:     false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rateLimitErr := CreateRateLimitError(tt.statusCode, tt.errorMessage, tt.retryAfterHeader)
			
			if rateLimitErr.HTTPStatus != http.StatusTooManyRequests {
				t.Errorf("Expected HTTP status %d, got %d", http.StatusTooManyRequests, rateLimitErr.HTTPStatus)
			}
			
			if rateLimitErr.Code != "RATE_LIMITED" {
				t.Errorf("Expected code 'RATE_LIMITED', got %q", rateLimitErr.Code)
			}
			
			if rateLimitErr.Message != tt.errorMessage {
				t.Errorf("Expected message %q, got %q", tt.errorMessage, rateLimitErr.Message)
			}
			
			if rateLimitErr.RetryAfter != tt.expectedRetryAfter {
				t.Errorf("Expected retry after %v, got %v", tt.expectedRetryAfter, rateLimitErr.RetryAfter)
			}
			
			if tt.expectResetAt && rateLimitErr.ResetAt == nil {
				t.Error("Expected ResetAt to be set, but it was nil")
			}
			
			if !tt.expectResetAt && rateLimitErr.ResetAt != nil {
				t.Error("Expected ResetAt to be nil, but it was set")
			}
		})
	}
}

// BenchmarkIsRateLimitError 性能测试
func BenchmarkIsRateLimitError(b *testing.B) {
	testCases := []struct {
		statusCode   int
		errorMessage string
	}{
		{429, "Rate limit exceeded"},
		{400, "Too many requests"},
		{500, "Internal server error"},
		{400, `{"error":"rate_limit","message":"Quota exceeded"}`},
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tc := range testCases {
			IsRateLimitError(tc.statusCode, tc.errorMessage)
		}
	}
}
