package models

import (
	"fmt"
	"time"
)

// GeminiLog represents a Gemini-specific processing log entry
type GeminiLog struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID       string    `gorm:"type:varchar(36);index" json:"request_id"`
	GroupID         uint      `gorm:"not null;index" json:"group_id"`
	GroupName       string    `gorm:"type:varchar(255);index" json:"group_name"`
	KeyValue        string    `gorm:"type:varchar(700);index" json:"key_value"`
	
	// 重试相关字段
	RetryCount      int       `gorm:"default:0" json:"retry_count"`
	InterruptReason string    `gorm:"type:varchar(50)" json:"interrupt_reason"`
	FinalSuccess    bool      `gorm:"default:false" json:"final_success"`
	
	// 内容相关字段
	AccumulatedText string    `gorm:"type:text" json:"accumulated_text"`
	ThoughtFiltered bool      `gorm:"default:false" json:"thought_filtered"`
	OutputChars     int       `gorm:"default:0" json:"output_chars"`
	
	// 性能相关字段
	TotalDuration   int64     `gorm:"default:0" json:"total_duration_ms"`
	RetryDuration   int64     `gorm:"default:0" json:"retry_duration_ms"`
	
	// 请求详情（可选，用于调试）
	OriginalRequest string    `gorm:"type:text" json:"original_request,omitempty"`
	RetryRequests   string    `gorm:"type:text" json:"retry_requests,omitempty"`
	
	// 错误信息
	ErrorMessage    string    `gorm:"type:text" json:"error_message,omitempty"`
	
	// 标准时间戳
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName returns the table name for GeminiLog
func (GeminiLog) TableName() string {
	return "gemini_logs"
}

// GeminiLogQueryParams represents query parameters for Gemini logs
type GeminiLogQueryParams struct {
	// 分页参数
	Page     int `json:"page" form:"page" binding:"min=1"`
	PageSize int `json:"page_size" form:"page_size" binding:"min=1,max=100"`
	
	// 过滤参数
	GroupID         *uint   `json:"group_id" form:"group_id"`
	GroupName       string  `json:"group_name" form:"group_name"`
	KeyValue        string  `json:"key_value" form:"key_value"`
	InterruptReason string  `json:"interrupt_reason" form:"interrupt_reason"`
	FinalSuccess    *bool   `json:"final_success" form:"final_success"`
	ThoughtFiltered *bool   `json:"thought_filtered" form:"thought_filtered"`
	
	// 重试次数范围
	MinRetryCount *int `json:"min_retry_count" form:"min_retry_count"`
	MaxRetryCount *int `json:"max_retry_count" form:"max_retry_count"`
	
	// 时间范围
	StartTime *time.Time `json:"start_time" form:"start_time"`
	EndTime   *time.Time `json:"end_time" form:"end_time"`
	
	// 排序参数
	OrderBy   string `json:"order_by" form:"order_by"`     // created_at, retry_count, total_duration
	OrderDesc bool   `json:"order_desc" form:"order_desc"` // true for DESC, false for ASC
}

// GeminiLogResponse represents the response for Gemini log queries
type GeminiLogResponse struct {
	Logs       []GeminiLog `json:"logs"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

// GeminiLogStats represents statistics for Gemini logs
type GeminiLogStats struct {
	// 基础统计
	TotalLogs       int64   `json:"total_logs"`
	SuccessfulLogs  int64   `json:"successful_logs"`
	FailedLogs      int64   `json:"failed_logs"`
	SuccessRate     float64 `json:"success_rate"`
	
	// 重试统计
	TotalRetries    int64   `json:"total_retries"`
	AverageRetries  float64 `json:"average_retries"`
	MaxRetries      int     `json:"max_retries"`
	
	// 思考过滤统计
	ThoughtFiltered int64   `json:"thought_filtered"`
	FilterRate      float64 `json:"filter_rate"`
	
	// 性能统计
	AverageDuration int64   `json:"average_duration_ms"`
	MaxDuration     int64   `json:"max_duration_ms"`
	MinDuration     int64   `json:"min_duration_ms"`
	
	// 中断原因统计
	InterruptionStats map[string]int64 `json:"interruption_stats"`
	
	// 时间范围
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// GeminiLogSummary represents a summary of Gemini log entry
type GeminiLogSummary struct {
	ID              uint      `json:"id"`
	RequestID       string    `json:"request_id"`
	GroupName       string    `json:"group_name"`
	RetryCount      int       `json:"retry_count"`
	InterruptReason string    `json:"interrupt_reason"`
	FinalSuccess    bool      `json:"final_success"`
	ThoughtFiltered bool      `json:"thought_filtered"`
	OutputChars     int       `json:"output_chars"`
	TotalDuration   int64     `json:"total_duration_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

// ToSummary converts GeminiLog to GeminiLogSummary
func (gl *GeminiLog) ToSummary() GeminiLogSummary {
	return GeminiLogSummary{
		ID:              gl.ID,
		RequestID:       gl.RequestID,
		GroupName:       gl.GroupName,
		RetryCount:      gl.RetryCount,
		InterruptReason: gl.InterruptReason,
		FinalSuccess:    gl.FinalSuccess,
		ThoughtFiltered: gl.ThoughtFiltered,
		OutputChars:     gl.OutputChars,
		TotalDuration:   gl.TotalDuration,
		CreatedAt:       gl.CreatedAt,
	}
}

// IsRetryAttempt returns true if this log represents a retry attempt
func (gl *GeminiLog) IsRetryAttempt() bool {
	return gl.RetryCount > 0
}

// HasThoughtContent returns true if thought content was filtered
func (gl *GeminiLog) HasThoughtContent() bool {
	return gl.ThoughtFiltered
}

// GetDurationSeconds returns duration in seconds
func (gl *GeminiLog) GetDurationSeconds() float64 {
	return float64(gl.TotalDuration) / 1000.0
}

// GetRetryDurationSeconds returns retry duration in seconds
func (gl *GeminiLog) GetRetryDurationSeconds() float64 {
	return float64(gl.RetryDuration) / 1000.0
}

// IsSuccessful returns true if the processing was successful
func (gl *GeminiLog) IsSuccessful() bool {
	return gl.FinalSuccess
}

// GetInterruptionCategory returns the category of interruption
func (gl *GeminiLog) GetInterruptionCategory() string {
	switch gl.InterruptReason {
	case "BLOCK":
		return "Content Blocked"
	case "DROP":
		return "Stream Dropped"
	case "INCOMPLETE":
		return "Incomplete Response"
	case "FINISH_ABNORMAL":
		return "Abnormal Finish"
	case "FINISH_DURING_THOUGHT":
		return "Finished During Thought"
	case "TIMEOUT":
		return "Timeout"
	default:
		return "Unknown"
	}
}

// Validate validates the GeminiLog fields
func (gl *GeminiLog) Validate() error {
	if gl.RequestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if gl.GroupID == 0 {
		return fmt.Errorf("group_id is required")
	}
	if gl.GroupName == "" {
		return fmt.Errorf("group_name is required")
	}
	if gl.RetryCount < 0 {
		return fmt.Errorf("retry_count cannot be negative")
	}
	if gl.OutputChars < 0 {
		return fmt.Errorf("output_chars cannot be negative")
	}
	if gl.TotalDuration < 0 {
		return fmt.Errorf("total_duration cannot be negative")
	}
	if gl.RetryDuration < 0 {
		return fmt.Errorf("retry_duration cannot be negative")
	}
	return nil
}

// SetDefaults sets default values for GeminiLog
func (gl *GeminiLog) SetDefaults() {
	if gl.CreatedAt.IsZero() {
		gl.CreatedAt = time.Now()
	}
	if gl.UpdatedAt.IsZero() {
		gl.UpdatedAt = time.Now()
	}
}
