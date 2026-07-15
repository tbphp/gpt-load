package i18n

import (
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

const (
	// LocalizerKey 是 gin.Context 中存储 Localizer 的键
	LocalizerKey = "localizer"
	// LangKey 是 gin.Context 中存储当前语言的键
	LangKey = "lang"
)

// Middleware i18n 中间件
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取 Accept-Language 头
		acceptLang := c.GetHeader("Accept-Language")

		// 获取 Localizer
		localizer := GetLocalizer(acceptLang)

		// 将 Localizer 存储到 Context 中
		c.Set(LocalizerKey, localizer)

		// 存储当前语言
		lang := normalizeLanguageCode(acceptLang)
		c.Set(LangKey, lang)

		c.Next()
	}
}

// GetLocalizerFromContext 从 gin.Context 获取 Localizer
func GetLocalizerFromContext(c *gin.Context) *i18n.Localizer {
	if localizer, exists := c.Get(LocalizerKey); exists {
		if l, ok := localizer.(*i18n.Localizer); ok {
			return l
		}
	}
	// 如果没有找到，返回默认的中文 Localizer
	return GetLocalizer("zh-CN")
}

// GetLangFromContext 从 gin.Context 获取当前语言
func GetLangFromContext(c *gin.Context) string {
	if lang, exists := c.Get(LangKey); exists {
		if l, ok := lang.(string); ok {
			return l
		}
	}
	return "zh-CN"
}

// Success 返回成功响应（带国际化消息）
func Success(c *gin.Context, msgID string, data any) {
	localizer := GetLocalizerFromContext(c)
	message := T(localizer, msgID)

	c.JSON(200, gin.H{
		"success": true,
		"message": message,
		"data":    data,
		"lang":    GetLangFromContext(c),
	})
}

// SuccessWithData 返回成功响应（带模板数据）
func SuccessWithData(c *gin.Context, msgID string, templateData map[string]any, data any) {
	localizer := GetLocalizerFromContext(c)
	message := T(localizer, msgID, templateData)

	c.JSON(200, gin.H{
		"success": true,
		"message": message,
		"data":    data,
		"lang":    GetLangFromContext(c),
	})
}

// Error 返回错误响应（带国际化消息）
func Error(c *gin.Context, code int, msgID string) {
	localizer := GetLocalizerFromContext(c)
	message := T(localizer, msgID)

	c.JSON(code, gin.H{
		"success": false,
		"message": message,
		"lang":    GetLangFromContext(c),
	})
}

// ErrorWithData 返回错误响应（带模板数据）
func ErrorWithData(c *gin.Context, code int, msgID string, templateData map[string]any) {
	localizer := GetLocalizerFromContext(c)
	message := T(localizer, msgID, templateData)

	c.JSON(code, gin.H{
		"success": false,
		"message": message,
		"lang":    GetLangFromContext(c),
	})
}

// Message 获取国际化消息
func Message(c *gin.Context, msgID string, templateData ...map[string]any) string {
	localizer := GetLocalizerFromContext(c)
	return T(localizer, msgID, templateData...)
}
