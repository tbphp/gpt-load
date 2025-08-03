package services

import (
	"encoding/csv"
	"fmt"
	"gpt-load/internal/models"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ExportableLogKey defines the structure for the data to be exported to CSV.
type ExportableLogKey struct {
	KeyValue   string `gorm:"column:key_value"`
	GroupName  string `gorm:"column:group_name"`
	StatusCode int    `gorm:"column:status_code"`
}

// LogService provides services related to request logs.
type LogService struct {
	DB *gorm.DB
}

// NewLogService creates a new LogService.
func NewLogService(db *gorm.DB) *LogService {
	return &LogService{DB: db}
}

// logFiltersScope returns a GORM scope function that applies filters from the Gin context.
func logFiltersScope(c *gin.Context) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if groupName := c.Query("group_name"); groupName != "" {
			db = db.Where("group_name LIKE ?", "%"+groupName+"%")
		}
		if keyValue := c.Query("key_value"); keyValue != "" {
			// 安全地处理 keyValue，避免越界错误
			var likePattern string
			if len(keyValue) > 2 {
				likePattern = "%" + keyValue[1:len(keyValue)-1] + "%"
			} else {
				likePattern = "%" + keyValue + "%"
			}
			db = db.Where("key_value LIKE ?", likePattern)
		}
		if model := c.Query("model"); model != "" {
			db = db.Where("model LIKE ?", "%"+model+"%")
		}
		if isSuccessStr := c.Query("is_success"); isSuccessStr != "" {
			if isSuccess, err := strconv.ParseBool(isSuccessStr); err == nil {
				db = db.Where("is_success = ?", isSuccess)
			}
		}
		if statusCodeStr := c.Query("status_code"); statusCodeStr != "" {
			if statusCode, err := strconv.Atoi(statusCodeStr); err == nil {
				db = db.Where("status_code = ?", statusCode)
			}
		}
		if sourceIP := c.Query("source_ip"); sourceIP != "" {
			db = db.Where("source_ip = ?", sourceIP)
		}
		if errorContains := c.Query("error_contains"); errorContains != "" {
			db = db.Where("error_message LIKE ?", "%"+errorContains+"%")
		}
		if startTimeStr := c.Query("start_time"); startTimeStr != "" {
			if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
				db = db.Where("timestamp >= ?", startTime)
			}
		}
		if endTimeStr := c.Query("end_time"); endTimeStr != "" {
			if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
				db = db.Where("timestamp <= ?", endTime)
			}
		}
		return db
	}
}

// GetLogsQuery returns a GORM query for fetching logs with filters.
func (s *LogService) GetLogsQuery(c *gin.Context) *gorm.DB {
	return s.DB.Model(&models.RequestLog{}).Scopes(logFiltersScope(c))
}

// StreamLogKeysToCSV fetches unique keys from logs based on filters and streams them as a CSV.
func (s *LogService) StreamLogKeysToCSV(c *gin.Context, writer io.Writer) error {
	// Create a CSV writer
	csvWriter := csv.NewWriter(writer)
	defer csvWriter.Flush()

	// Write CSV header
	header := []string{"key_value", "group_name", "status_code"}
	if err := csvWriter.Write(header); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	var results []ExportableLogKey

	baseQuery := s.DB.Model(&models.RequestLog{}).Scopes(logFiltersScope(c))

	// 使用窗口函数获取每个key_value的最新记录
	err := s.DB.Raw(`
		SELECT
			key_value,
			group_name,
			status_code
		FROM (
			SELECT
				key_value,
				group_name,
				status_code,
				ROW_NUMBER() OVER (PARTITION BY key_value ORDER BY timestamp DESC) as rn
			FROM (?) as filtered_logs
		) ranked
		WHERE rn = 1
		ORDER BY key_value
	`, baseQuery).Scan(&results).Error

	if err != nil {
		return fmt.Errorf("failed to fetch log keys: %w", err)
	}

	// 写入CSV数据
	for _, record := range results {
		csvRecord := []string{
			record.KeyValue,
			record.GroupName,
			strconv.Itoa(record.StatusCode),
		}
		if err := csvWriter.Write(csvRecord); err != nil {
			return fmt.Errorf("failed to write CSV record: %w", err)
		}
	}

	return nil
}
