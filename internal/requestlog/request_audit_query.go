package requestlog

import (
	"fmt"

	"gorm.io/gorm"
)

// 直接筛选日志中冻结的审查结果，规则删除或改名后仍可查询历史记录。
func applyRequestAuditFilters(query *gorm.DB, input ListQuery) (*gorm.DB, error) {
	if input.AuditStatus == "" && input.AuditRule == "" {
		return query, nil
	}
	var status, mode, findings, name, findingStatus, contains string
	switch query.Dialector.Name() {
	case "sqlite":
		status = "json_extract(request_logs.request_audit, '$.status')"
		mode = "json_extract(request_logs.request_audit, '$.mode')"
		findings = "json_each(request_logs.request_audit, '$.findings') AS audit_finding"
		name = "json_extract(audit_finding.value, '$.name')"
		findingStatus = "json_extract(audit_finding.value, '$.status')"
		contains = "instr"
	case "mysql":
		status = "JSON_UNQUOTE(JSON_EXTRACT(request_logs.request_audit, '$.status'))"
		mode = "JSON_UNQUOTE(JSON_EXTRACT(request_logs.request_audit, '$.mode'))"
		findings = "JSON_TABLE(request_logs.request_audit, '$.findings[*]' COLUMNS (rule_name TEXT PATH '$.name', rule_status VARCHAR(32) PATH '$.status')) AS audit_finding"
		name = "audit_finding.rule_name"
		findingStatus = "audit_finding.rule_status"
		contains = "instr"
	case "postgres":
		status = "request_logs.request_audit::jsonb->>'status'"
		mode = "request_logs.request_audit::jsonb->>'mode'"
		findings = "jsonb_array_elements(COALESCE(NULLIF(request_logs.request_audit::jsonb->'findings', 'null'::jsonb), '[]'::jsonb)) AS audit_finding(value)"
		name = "audit_finding.value->>'name'"
		findingStatus = "audit_finding.value->>'status'"
		contains = "strpos"
	default:
		return nil, fmt.Errorf("unsupported guardrail filter database: %s", query.Dialector.Name())
	}
	if input.AuditStatus != "" {
		// 与旧实验日志的读取语义一致：matched 按旧模式映射为告警或拦截。
		outcome := fmt.Sprintf("CASE WHEN COALESCE(%s, '') <> '' AND %s = 'matched' THEN CASE WHEN %s = 'enforce' THEN 'blocked' ELSE 'warned' END ELSE %s END", mode, status, mode, status)
		query = query.Where("("+outcome+") = ?", input.AuditStatus)
	}
	if input.AuditRule != "" {
		// 使用字面子串匹配，百分号、下划线等字符不会被当成通配符。
		condition := fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s(LOWER(%s), LOWER(?)) > 0 AND (COALESCE(%s, '') = '' OR %s = 'matched'))", findings, contains, name, mode, findingStatus)
		query = query.Where(condition, input.AuditRule)
	}
	return query, nil
}
