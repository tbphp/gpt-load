package i18n

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gpt-load/internal/platform/i18n/locales"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

const (
	// Accept-Language 只用于文案协商，超限时安全回退默认语言。
	defaultLanguage          = "zh-CN"
	maxAcceptLanguageBytes   = 4 << 10
	maxAcceptLanguageEntries = 32
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
	return newLocalizer(parseAcceptLanguage(acceptLang))
}

func newLocalizer(languages []string) *i18n.Localizer {
	if len(languages) == 0 {
		languages = []string{defaultLanguage}
	}
	return i18n.NewLocalizer(bundle, languages...)
}

// ResolveLanguage returns the canonical supported language selected from
// Accept-Language.
func ResolveLanguage(acceptLang string) string {
	return primaryLanguage(parseAcceptLanguage(acceptLang))
}

func primaryLanguage(languages []string) string {
	if len(languages) == 0 {
		return defaultLanguage
	}
	return languages[0]
}

// parseAcceptLanguage 解析 Accept-Language 头
func parseAcceptLanguage(acceptLang string) []string {
	if acceptLang == "" || len(acceptLang) > maxAcceptLanguageBytes {
		return nil
	}
	entryCount := strings.Count(acceptLang, ",") + 1
	if entryCount > maxAcceptLanguageEntries {
		return nil
	}

	type preference struct {
		tag    language.Tag
		weight float32
	}

	preferences := make([]preference, 0, entryCount)
	for _, entry := range strings.Split(acceptLang, ",") {
		tags, weights, err := language.ParseAcceptLanguage(entry)
		if err != nil || len(tags) == 0 {
			continue
		}
		preferences = append(preferences, preference{tag: tags[0], weight: weights[0]})
	}
	sort.SliceStable(preferences, func(i, j int) bool {
		return preferences[i].weight > preferences[j].weight
	})

	seen := make(map[string]struct{}, len(preferences))
	result := make([]string, 0, len(preferences))
	for _, preference := range preferences {
		base, _ := preference.tag.Base()
		var supported string
		switch base.String() {
		case "mul", "zh":
			supported = defaultLanguage
		case "en":
			supported = "en-US"
		case "ja":
			supported = "ja-JP"
		default:
			continue
		}
		if _, exists := seen[supported]; exists {
			continue
		}
		seen[supported] = struct{}{}
		result = append(result, supported)
	}
	return result
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
