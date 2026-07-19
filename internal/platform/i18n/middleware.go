package i18n

import (
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

const (
	// LocalizerKey 是 gin.Context 中存储 Localizer 的键
	LocalizerKey = "localizer"
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

// Message 获取国际化消息
func Message(c *gin.Context, msgID string, templateData ...map[string]any) string {
	localizer := GetLocalizerFromContext(c)
	return T(localizer, msgID, templateData...)
}
