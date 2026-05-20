package handler

import (
	"fmt"
	"strings"

	"gpt-load/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Metrics returns a Prometheus-text /metrics endpoint exposing token usage
// and request counts aggregated from request_logs.
//
// This is deliberately kept minimal — a full Prometheus client library is not
// introduced.  The output format follows the Prometheus exposition format so
// operators can scrape it with any standard Prometheus server and build
// dashboards (e.g. Grafana) on top.
func (s *Server) Metrics(c *gin.Context) {
	var results []struct {
		GroupName        string
		Model            string
		TotalRequests    int64
		TotalTokens      int64
		TotalCost        float64
		TotalPrompt      int64
		TotalCompletion  int64
	}

	// Aggregate token usage and request count from successful non-streaming
	// requests (those are the ones where we can extract usage data).
	if err := s.DB.Model(&models.RequestLog{}).
		Select(`COALESCE(group_name, '') as group_name,
		        COALESCE(model, 'unknown') as model,
		        COUNT(*) as total_requests,
		        COALESCE(SUM(total_tokens), 0) as total_tokens,
		        COALESCE(SUM(token_cost_usd), 0) as total_cost,
		        COALESCE(SUM(prompt_tokens), 0) as total_prompt,
		        COALESCE(SUM(completion_tokens), 0) as total_completion`).
		Where("is_success = ? AND is_stream = ?", true, false).
		Group("group_name, model").
		Scan(&results).Error; err != nil {
		logrus.WithError(err).Error("Failed to query metrics")
		c.String(500, "internal error\n")
		return
	}

	var sb strings.Builder
	sb.WriteString("# HELP gpt_load_requests_total Total number of successful proxy requests by group and model\n")
	sb.WriteString("# TYPE gpt_load_requests_total counter\n")
	for _, r := range results {
		sb.WriteString(fmt.Sprintf(
			`gpt_load_requests_total{group=%q,model=%q} %d`+"\n",
			r.GroupName, r.Model, r.TotalRequests,
		))
	}

	sb.WriteString("\n# HELP gpt_load_tokens_total Total token count by type, group, and model\n")
	sb.WriteString("# TYPE gpt_load_tokens_total counter\n")
	for _, r := range results {
		if r.TotalPrompt > 0 {
			sb.WriteString(fmt.Sprintf(
				`gpt_load_tokens_total{type="prompt",group=%q,model=%q} %d`+"\n",
				r.GroupName, r.Model, r.TotalPrompt,
			))
		}
		if r.TotalCompletion > 0 {
			sb.WriteString(fmt.Sprintf(
				`gpt_load_tokens_total{type="completion",group=%q,model=%q} %d`+"\n",
				r.GroupName, r.Model, r.TotalCompletion,
			))
		}
		if r.TotalTokens > 0 {
			sb.WriteString(fmt.Sprintf(
				`gpt_load_tokens_total{type="total",group=%q,model=%q} %d`+"\n",
				r.GroupName, r.Model, r.TotalTokens,
			))
		}
	}

	sb.WriteString("\n# HELP gpt_load_cost_total Total cost in USD by group and model\n")
	sb.WriteString("# TYPE gpt_load_cost_total counter\n")
	for _, r := range results {
		if r.TotalCost > 0 {
			sb.WriteString(fmt.Sprintf(
				`gpt_load_cost_total{group=%q,model=%q} %.6f`+"\n",
				r.GroupName, r.Model, r.TotalCost,
			))
		}
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(200, sb.String())
}
