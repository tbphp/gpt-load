package i18n

import (
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

const (
	// LocalizerKey 是 gin.Context 中存储 Localizer 的键
	LocalizerKey = "localizer"
	// LanguageKey 是 gin.Context 中存储规范语言代码的键
	LanguageKey = "language"
)

// AttachRequestLanguage resolves and stores the request language on a context.
func AttachRequestLanguage(c *gin.Context) {
	if c == nil {
		return
	}
	acceptLang := c.GetHeader("Accept-Language")
	languages := parseAcceptLanguage(acceptLang)
	c.Set(LocalizerKey, newLocalizer(languages))
	c.Set(LanguageKey, primaryLanguage(languages))
}

// Middleware i18n 中间件
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		AttachRequestLanguage(c)
		c.Next()
	}
}

// GetLanguageFromContext 返回当前响应应使用的规范语言代码。
func GetLanguageFromContext(c *gin.Context) string {
	if value, exists := c.Get(LanguageKey); exists {
		if language, ok := value.(string); ok && language != "" {
			return language
		}
	}
	return ResolveLanguage(c.GetHeader("Accept-Language"))
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

// Message 获取国际化消息
func Message(c *gin.Context, msgID string, templateData ...map[string]any) string {
	localizer := GetLocalizerFromContext(c)
	return T(localizer, msgID, templateData...)
}
