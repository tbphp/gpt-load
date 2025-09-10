package i18n

import (
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/i18n/locales"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var (
	bundle *i18n.Bundle
)

// Init 初始化 i18n
func Init() error {
	bundle = i18n.NewBundle(language.Chinese)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// 加载支持的语言文件
	languages := []string{"zh-CN", "en-US", "ja-JP"}
	for _, lang := range languages {
		if err := loadMessageFile(lang); err != nil {
			return fmt.Errorf("failed to load language file %s: %w", lang, err)
		}
	}

	return nil
}

// loadMessageFile 加载语言文件
func loadMessageFile(lang string) error {
	// 根据语言设置消息
	messages := getMessages(lang)
	for id, msg := range messages {
		bundle.AddMessages(language.MustParse(lang), &i18n.Message{
			ID:    id,
			Other: msg,
		})
	}

	return nil
}

// GetLocalizer 获取本地化器
func GetLocalizer(acceptLang string) *i18n.Localizer {
	// 解析 Accept-Language 头
	langs := parseAcceptLanguage(acceptLang)

	// 如果没有指定语言，默认使用中文
	if len(langs) == 0 {
		langs = []string{"zh-CN"}
	}

	return i18n.NewLocalizer(bundle, langs...)
}

// parseAcceptLanguage 解析 Accept-Language 头
func parseAcceptLanguage(acceptLang string) []string {
	if acceptLang == "" {
		return nil
	}

	// 简单解析，只取第一个语言
	parts := strings.Split(acceptLang, ",")
	if len(parts) > 0 {
		lang := strings.TrimSpace(parts[0])
		// 移除质量因子 (q=...)
		if idx := strings.Index(lang, ";"); idx > 0 {
			lang = lang[:idx]
		}

		// 标准化语言代码
		lang = normalizeLanguageCode(lang)
		return []string{lang}
	}

	return nil
}

// normalizeLanguageCode 标准化语言代码
func normalizeLanguageCode(lang string) string {
	lang = strings.TrimSpace(lang)

	// 映射常见的语言代码
	switch strings.ToLower(lang) {
	case "zh", "zh-cn", "zh-hans":
		return "zh-CN"
	case "en", "en-us":
		return "en-US"
	case "ja", "ja-jp":
		return "ja-JP"
	default:
		// 尝试匹配前缀
		if strings.HasPrefix(strings.ToLower(lang), "zh") {
			return "zh-CN"
		}
		if strings.HasPrefix(strings.ToLower(lang), "en") {
			return "en-US"
		}
		if strings.HasPrefix(strings.ToLower(lang), "ja") {
			return "ja-JP"
		}
		// 默认返回中文
		return "zh-CN"
	}
}

// T 翻译消息
func T(localizer *i18n.Localizer, msgID string, data ...map[string]any) string {
	config := &i18n.LocalizeConfig{
		MessageID: msgID,
	}

	if len(data) > 0 {
		config.TemplateData = data[0]
	}

	msg, err := localizer.Localize(config)
	if err != nil {
		// 如果翻译失败，返回消息ID
		return msgID
	}

	return msg
}

// getMessages 获取语言消息
func getMessages(lang string) map[string]string {
	switch lang {
	case "en-US":
		return locales.MessagesEnUS
	case "ja-JP":
		return locales.MessagesJaJP
	default:
		return locales.MessagesZhCN
	}
}
