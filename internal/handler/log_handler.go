package handler

import (
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/i18n"
	"gpt-load/internal/models"
	"gpt-load/internal/response"
	"gpt-load/internal/utils"
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// LogResponse defines the structure for log entries in the API response
type LogResponse struct {
	models.RequestLog
}

// GetLogs handles fetching request logs with filtering and pagination.
func (s *Server) GetLogs(c *gin.Context) {
	// 检查是否处于蜜罐模式
	if utils.IsHoneypotMode(c) {
		mode := utils.GetHoneypotMode(c)
		settings := s.SettingsManager.GetSettings()

		// 生成蜜罐日志数据
		generator := utils.NewHoneypotDataGenerator(mode, settings.HoneypotSeed)

		// 获取分页参数
		pageStr := c.DefaultQuery("page", "1")
		pageSizeStr := c.DefaultQuery("page_size", "20")
		page, _ := strconv.Atoi(pageStr)
		pageSize, _ := strconv.Atoi(pageSizeStr)
		if page < 1 {
			page = 1
		}
		if pageSize < 1 || pageSize > 100 {
			pageSize = 20
		}

		// 生成足够的日志数据
		totalLogs := 1000 // 模拟总共1000条日志
		logs := generator.GenerateRequestLogs(totalLogs)

		// 模拟分页
		start := (page - 1) * pageSize
		end := start + pageSize
		if start >= len(logs) {
			logs = []models.RequestLog{}
		} else if end > len(logs) {
			logs = logs[start:]
		} else {
			logs = logs[start:end]
		}

		pagination := &response.PaginatedResponse{
			Items: logs,
			Pagination: response.Pagination{
				Page:       page,
				PageSize:   pageSize,
				TotalItems: int64(totalLogs),
				TotalPages: (totalLogs + pageSize - 1) / pageSize,
			},
		}

		response.Success(c, pagination)
		return
	}

	query := s.LogService.GetLogsQuery(c)

	var logs []models.RequestLog
	query = query.Order("timestamp desc")
	pagination, err := response.Paginate(c, query, &logs)
	if err != nil {
		response.Error(c, app_errors.ParseDBError(err))
		return
	}

	// 解密所有日志中的密钥用于前端显示
	for i := range logs {
		if logs[i].KeyValue != "" {
			decryptedValue, err := s.EncryptionSvc.Decrypt(logs[i].KeyValue)
			if err != nil {
				logrus.WithError(err).WithField("log_id", logs[i].ID).Error("Failed to decrypt log key value")
				logs[i].KeyValue = "failed-to-decrypt"
			} else {
				logs[i].KeyValue = decryptedValue
			}
		}
	}

	pagination.Items = logs
	response.Success(c, pagination)
}

// ExportLogs handles exporting filtered log keys to a CSV file.
func (s *Server) ExportLogs(c *gin.Context) {
	filename := fmt.Sprintf("log_keys_export_%s.csv", time.Now().Format("20060102150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "text/csv; charset=utf-8")

	// Stream the response
	err := s.LogService.StreamLogKeysToCSV(c, c.Writer)
	if err != nil {
		log.Printf("Failed to stream log keys to CSV: %v", err)
		c.JSON(500, gin.H{"error": i18n.Message(c, "error.export_logs")})
		return
	}
}
