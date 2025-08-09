package models

// SystemSettingInfo 表示系统配置的详细信息（用于API返回）
type SystemSettingInfo struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Value        any      `json:"value"`
	Type         string   `json:"type"` // "int", "bool", "string"
	DefaultValue any      `json:"default_value"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	MinValue     *int     `json:"min_value,omitempty"`
	Required     bool     `json:"required"`
}

// CategorizedSettings a list of settings grouped by category
type CategorizedSettings struct {
	CategoryName string              `json:"category_name"`
	Settings     []SystemSettingInfo `json:"settings"`
}
