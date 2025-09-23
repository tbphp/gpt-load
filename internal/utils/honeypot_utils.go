package utils

import (
	"crypto/md5"
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"math/rand"
	"strings"
	"time"
)

// HoneypotDataGenerator 蜜罐数据生成器
type HoneypotDataGenerator struct {
	mode string
	seed int64
	rng  *rand.Rand
}

// NewHoneypotDataGenerator 创建蜜罐数据生成器
func NewHoneypotDataGenerator(mode string, customSeed string) *HoneypotDataGenerator {
	// 使用当前日期作为基础种子，确保同一天的数据一致
	today := time.Now().Format("2006-01-02")

	// 如果有自定义种子，加入到计算中
	seedStr := today
	if customSeed != "" {
		seedStr = today + "-" + customSeed
	}

	// 使用MD5生成数值种子
	hash := md5.Sum([]byte(seedStr))
	seed := int64(0)
	for i := 0; i < 8; i++ {
		seed = (seed << 8) | int64(hash[i])
	}

	return &HoneypotDataGenerator{
		mode: mode,
		seed: seed,
		rng:  rand.New(rand.NewSource(seed)),
	}
}

// GenerateGroups 生成蜜罐分组数据（不包含API密钥以提高性能）
func (g *HoneypotDataGenerator) GenerateGroups() []models.Group {
	// 生成4-10个分组
	groupCount := g.rng.Intn(7) + 4
	groups := make([]models.Group, groupCount)

	// 预定义的分组配置池，从中随机选择和组合
	groupConfigs := []struct {
		namePrefix   string
		serviceName  string
		displayName  string
		channelType  string
		testModel    string
	}{
		{"openai", "openai", "OpenAI", "openai", "gpt-4"},
		{"openai", "openai", "OpenAI GPT", "openai", "gpt-4-turbo"},
		{"openai", "openai", "OpenAI API", "openai", "gpt-3.5-turbo"},
		{"claude", "anthropic", "Claude", "anthropic", "claude-3-opus"},
		{"claude", "anthropic", "Claude AI", "anthropic", "claude-3-sonnet"},
		{"anthropic", "anthropic", "Anthropic", "anthropic", "claude-3-haiku"},
		{"gemini", "googleapis", "Gemini", "gemini", "gemini-pro"},
		{"gemini", "googleapis", "Google AI", "gemini", "gemini-1.5-pro"},
		{"gemini", "googleapis", "Bard API", "gemini", "gemini-pro-vision"},
		{"azure", "azure.openai", "Azure OpenAI", "openai", "gpt-4"},
		{"azure", "azure.openai", "Microsoft OpenAI", "openai", "gpt-35-turbo"},
		{"azure", "azure.openai", "Azure GPT", "openai", "gpt-4-32k"},
		{"cohere", "cohere", "Cohere", "openai", "command-r"},
		{"cohere", "cohere", "Command AI", "openai", "command-r-plus"},
		{"perplexity", "perplexity", "Perplexity", "openai", "llama-3-70b"},
		{"perplexity", "perplexity", "PPLX", "openai", "mixtral-8x7b"},
		{"groq", "groq", "Groq", "openai", "llama3-70b"},
		{"groq", "groq", "Groq Lightning", "openai", "mixtral-8x7b"},
		{"deepseek", "deepseek", "DeepSeek", "openai", "deepseek-chat"},
		{"deepseek", "deepseek", "DeepSeek AI", "openai", "deepseek-coder"},
		{"qwen", "qwen", "Qwen", "openai", "qwen-max"},
		{"qwen", "qwen", "通义千问", "openai", "qwen-turbo"},
		{"qwen", "qwen", "Alibaba Qwen", "openai", "qwen-plus"},
		{"mistral", "mistral", "Mistral AI", "openai", "mistral-large"},
		{"mistral", "mistral", "Mistral", "openai", "mixtral-8x22b"},
		{"together", "together", "Together AI", "openai", "llama-3-70b"},
		{"together", "together", "Together", "openai", "mixtral-8x7b"},
		{"replicate", "replicate", "Replicate", "openai", "llama-3-70b"},
		{"replicate", "replicate", "Replicate AI", "openai", "mixtral-8x7b"},
		{"huggingface", "huggingface", "Hugging Face", "openai", "meta-llama-3-70b"},
		{"huggingface", "huggingface", "HF Inference", "openai", "mistralai/mixtral-8x7b"},
		{"fireworks", "fireworks", "Fireworks AI", "openai", "llama-3-70b"},
		{"anyscale", "anyscale", "Anyscale", "openai", "meta-llama/llama-3-70b"},
		{"xinference", "xinference", "Xinference", "openai", "qwen-chat"},
		{"localai", "localai", "LocalAI", "openai", "gpt-3.5-turbo"},
	}

	// 随机打乱分组配置
	configIndices := make([]int, len(groupConfigs))
	for i := range configIndices {
		configIndices[i] = i
	}
	for i := len(configIndices) - 1; i > 0; i-- {
		j := g.rng.Intn(i + 1)
		configIndices[i], configIndices[j] = configIndices[j], configIndices[i]
	}

	// 生成随机的分组名后缀
	suffixes := []string{"", "-prod", "-dev", "-api", "-v1", "-v2", "-main", "-primary", "-backup", "-cluster1", "-cluster2"}

	for i := 0; i < groupCount; i++ {
		config := groupConfigs[configIndices[i]]

		// 随机生成分组名
		suffix := ""
		if g.rng.Float64() < 0.6 { // 60%概率添加后缀
			suffix = suffixes[g.rng.Intn(len(suffixes))]
		}

		// 30%概率添加随机数字
		if g.rng.Float64() < 0.3 {
			suffix += fmt.Sprintf("-%d", g.rng.Intn(99)+1)
		}

		groupName := config.namePrefix + suffix

		now := time.Now()
		lastValidated := now.Add(-time.Duration(g.rng.Intn(24)) * time.Hour)

		group := models.Group{
			ID:                 uint(i + 1),
			Name:               groupName,
			DisplayName:        config.displayName,
			ProxyKeys:          g.generateRealisticProxyKey(config.serviceName),
			Description:        fmt.Sprintf("%s API服务", config.displayName),
			Upstreams:          []byte(fmt.Sprintf(`[{"url":"https://api.%s.com","weight":100}]`, g.getRealDomain(config.serviceName))),
			ValidationEndpoint: fmt.Sprintf("/v1/models"),
			ChannelType:        config.channelType,
			Sort:               i,
			TestModel:          config.testModel,
			// 不在这里生成APIKeys，提高性能。需要时再单独生成
			LastValidatedAt:    &lastValidated,
			CreatedAt:          now.Add(-time.Duration(g.rng.Intn(30*24)) * time.Hour), // 随机30天内创建
			UpdatedAt:          now.Add(-time.Duration(g.rng.Intn(7*24)) * time.Hour),  // 随机7天内更新
		}

		// 生成ProxyKeysMap
		group.ProxyKeysMap = make(map[string]struct{})
		keys := strings.Split(group.ProxyKeys, ",")
		for _, key := range keys {
			group.ProxyKeysMap[strings.TrimSpace(key)] = struct{}{}
		}

		groups[i] = group
	}

	return groups
}

// GenerateKeysForGroup 为分组生成API密钥（支持分页以提高性能）
func (g *HoneypotDataGenerator) GenerateKeysForGroup(groupID uint, page, size int) ([]models.APIKey, int64) {
	var totalKeyCount int64
	var keyLength int

	switch g.mode {
	case types.HoneypotModeDeceive:
		// 忽悠模式：20-40个正常长度的key
		totalKeyCount = int64(g.rng.Intn(21) + 20) // 20-40个
		keyLength = 48                  // 正常长度
	case types.HoneypotModeOverload:
		// 塞满模式：数万个超长key
		totalKeyCount = int64(g.rng.Intn(50000) + 50000) // 50000-99999个
		keyLength = g.rng.Intn(500) + 200    // 200-699个字符的超长key
	default:
		totalKeyCount = 25
		keyLength = 48
	}

	// 计算分页
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}

	offset := (page - 1) * size
	if int64(offset) >= totalKeyCount {
		return []models.APIKey{}, totalKeyCount
	}

	// 只生成当前页面需要的密钥
	actualSize := size
	if int64(offset+size) > totalKeyCount {
		actualSize = int(totalKeyCount) - offset
	}

	keys := make([]models.APIKey, actualSize)
	now := time.Now()

	// 创建一个基于分页偏移量的独立随机数生成器，确保相同分页返回相同结果
	pageRng := rand.New(rand.NewSource(g.seed + int64(groupID)*1000 + int64(offset)))

	for i := 0; i < actualSize; i++ {
		keyID := uint(offset + i + 1)

		// 根据分组生成真实格式的密钥
		var keyValue string
		switch groupID % 7 {
		case 1: // OpenAI 格式
			keyValue = g.generateRealisticKeyWithRNG("openai", keyLength, pageRng)
		case 2: // Anthropic 格式
			keyValue = g.generateRealisticKeyWithRNG("anthropic", keyLength, pageRng)
		case 3: // Groq 格式
			keyValue = g.generateRealisticKeyWithRNG("groq", keyLength, pageRng)
		case 4: // Gemini 格式
			keyValue = g.generateRealisticKeyWithRNG("gemini", keyLength, pageRng)
		case 5: // Azure 格式
			keyValue = g.generateRealisticKeyWithRNG("azure", keyLength, pageRng)
		case 6: // Perplexity 格式
			keyValue = g.generateRealisticKeyWithRNG("perplexity", keyLength, pageRng)
		default: // 默认 OpenAI 格式
			keyValue = g.generateRealisticKeyWithRNG("openai", keyLength, pageRng)
		}

		// 模拟不同的密钥状态
		status := models.KeyStatusActive
		if pageRng.Float64() < 0.1 { // 10%的密钥是无效的
			status = models.KeyStatusInvalid
		}

		// 模拟使用次数和失败次数
		requestCount := int64(pageRng.Intn(10000))
		failureCount := int64(0)
		if status == models.KeyStatusInvalid {
			failureCount = int64(pageRng.Intn(10) + 3) // 3-12次失败
		}

		lastUsed := now.Add(-time.Duration(pageRng.Intn(7*24)) * time.Hour)

		keys[i] = models.APIKey{
			ID:           keyID,
			KeyValue:     keyValue,
			KeyHash:      generateKeyHash(keyValue),
			GroupID:      groupID,
			Status:       status,
			RequestCount: requestCount,
			FailureCount: failureCount,
			LastUsedAt:   &lastUsed,
			CreatedAt:    now.Add(-time.Duration(pageRng.Intn(30*24)) * time.Hour),
			UpdatedAt:    now.Add(-time.Duration(pageRng.Intn(7*24)) * time.Hour),
		}
	}

	return keys, totalKeyCount
}

// GenerateDashboardStats 生成仪表盘统计数据
func (g *HoneypotDataGenerator) GenerateDashboardStats() models.DashboardStatsResponse {
	var totalKeys int64
	switch g.mode {
	case types.HoneypotModeDeceive:
		totalKeys = int64(g.rng.Intn(200) + 100) // 100-299个key
	case types.HoneypotModeOverload:
		totalKeys = int64(g.rng.Intn(500000) + 500000) // 50万-100万个key
	default:
		totalKeys = 150
	}

	rpm := int64(g.rng.Intn(1000) + 100)        // 100-1099 RPM
	requestCount := int64(g.rng.Intn(50000) + 10000) // 1万-6万请求
	errorRate := float64(g.rng.Intn(10)) + 0.5   // 0.5-9.5%错误率

	// 生成正常的安全警告，完全不暴露蜜罐信息
	securityWarnings := []models.SecurityWarning{}

	// 添加一些正常的安全警告
	if g.rng.Float64() < 0.8 { // 80%概率显示加密警告
		securityWarnings = append(securityWarnings, models.SecurityWarning{
			Type:       "ENCRYPTION_KEY",
			Message:    "建议设置ENCRYPTION_KEY以加密保护API密钥",
			Severity:   "medium",
			Suggestion: "在环境变量中配置ENCRYPTION_KEY以提高安全性",
		})
	}

	if g.rng.Float64() < 0.6 { // 60%概率显示认证警告
		securityWarnings = append(securityWarnings, models.SecurityWarning{
			Type:       "AUTH_KEY",
			Message:    "建议使用更强的认证密钥",
			Severity:   "low",
			Suggestion: "使用至少32个字符的强密码提高安全性",
		})
	}

	if g.rng.Float64() < 0.4 { // 40%概率显示代理警告
		securityWarnings = append(securityWarnings, models.SecurityWarning{
			Type:       "PROXY_KEY",
			Message:    "建议定期更新代理密钥以确保安全",
			Severity:   "low",
			Suggestion: "定期更换代理密钥可以降低安全风险",
		})
	}

	return models.DashboardStatsResponse{
		KeyCount: models.StatCard{
			Value:         float64(totalKeys),
			SubValue:      int64(g.rng.Intn(10) + 5), // 5-14个分组
			SubValueTip:   "活跃分组",
			Trend:         float64(g.rng.Intn(20) - 10), // -10到10的趋势
			TrendIsGrowth: g.rng.Float64() > 0.5,
		},
		RPM: models.StatCard{
			Value:         float64(rpm),
			Trend:         float64(g.rng.Intn(50) - 25),
			TrendIsGrowth: g.rng.Float64() > 0.4,
		},
		RequestCount: models.StatCard{
			Value:         float64(requestCount),
			SubValue:      requestCount / 24, // 估算每小时请求数
			SubValueTip:   "每小时平均",
			Trend:         float64(g.rng.Intn(30) - 15),
			TrendIsGrowth: g.rng.Float64() > 0.6,
		},
		ErrorRate: models.StatCard{
			Value:         errorRate,
			Trend:         float64(g.rng.Intn(4) - 2),
			TrendIsGrowth: g.rng.Float64() > 0.7, // 错误率增长概率较低
		},
		SecurityWarnings: securityWarnings,
	}
}

// GenerateRequestLogs 生成请求日志数据
func (g *HoneypotDataGenerator) GenerateRequestLogs(limit int) []models.RequestLog {
	if limit <= 0 {
		limit = 50
	}

	logs := make([]models.RequestLog, limit)

	models_list := []string{"gpt-4", "gpt-3.5-turbo", "claude-3-opus", "claude-3-sonnet", "gemini-pro", "gemini-1.5-pro"}
	paths := []string{"/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/models", "/v1/messages"}
	userAgents := []string{"curl/7.68.0", "python-requests/2.31.0", "node-fetch/3.3.0", "Mozilla/5.0 (compatible; Bot/1.0)"}
	sourceIPs := []string{"192.168.1.100", "10.0.0.50", "172.16.0.25", "203.0.113.42", "198.51.100.88"}

	for i := 0; i < limit; i++ {
		isSuccess := g.rng.Float64() > 0.1 // 90%成功率
		statusCode := 200
		errorMessage := ""

		if !isSuccess {
			statusCodes := []int{400, 401, 403, 429, 500, 502, 503}
			statusCode = statusCodes[g.rng.Intn(len(statusCodes))]
			errorMessages := []string{
				"Invalid API key",
				"Rate limit exceeded",
				"Model not found",
				"Request timeout",
				"Internal server error",
			}
			errorMessage = errorMessages[g.rng.Intn(len(errorMessages))]
		}

		timestamp := time.Now().Add(-time.Duration(g.rng.Intn(7*24*60)) * time.Minute) // 最近7天
		groupID := uint(g.rng.Intn(5) + 1)

		// 生成真实格式的密钥
		keyValue := g.generateRealisticKey("openai", 51)
		if g.rng.Float64() < 0.3 { // 30%概率使用其他格式
			keyTypes := []string{"anthropic", "groq", "gemini"}
			keyType := keyTypes[g.rng.Intn(len(keyTypes))]
			keyValue = g.generateRealisticKey(keyType, 48)
		}

		logs[i] = models.RequestLog{
			ID:           fmt.Sprintf("req_%d_%d", timestamp.Unix(), i),
			Timestamp:    timestamp,
			GroupID:      groupID,
			GroupName:    fmt.Sprintf("group_%d", groupID),
			KeyValue:     keyValue,
			KeyHash:      generateKeyHash(keyValue),
			Model:        models_list[g.rng.Intn(len(models_list))],
			IsSuccess:    isSuccess,
			SourceIP:     sourceIPs[g.rng.Intn(len(sourceIPs))],
			StatusCode:   statusCode,
			RequestPath:  paths[g.rng.Intn(len(paths))],
			Duration:     int64(g.rng.Intn(5000) + 100), // 100-5099ms
			ErrorMessage: errorMessage,
			UserAgent:    userAgents[g.rng.Intn(len(userAgents))],
			RequestType:  models.RequestTypeFinal,
			UpstreamAddr: fmt.Sprintf("api.example%d.com:443", g.rng.Intn(5)+1),
			IsStream:     g.rng.Float64() < 0.3, // 30%是流式请求
		}
	}

	return logs
}

// generateRandomString 生成指定长度的随机字符串
func generateRandomString(rng *rand.Rand, length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

// generateKeyHash 生成密钥哈希
func generateKeyHash(keyValue string) string {
	hash := md5.Sum([]byte(keyValue))
	return fmt.Sprintf("%x", hash)
}

// IsHoneypotMode 检查是否处于蜜罐模式
func IsHoneypotMode(c interface{}) bool {
	// 这里需要根据实际的上下文类型进行类型断言
	// 假设传入的是gin.Context
	if ctx, ok := c.(interface{ Get(string) (interface{}, bool) }); ok {
		if mode, exists := ctx.Get(types.HoneypotModeKey); exists {
			return mode != nil
		}
	}
	return false
}

// GetHoneypotMode 获取蜜罐模式
func GetHoneypotMode(c interface{}) string {
	if ctx, ok := c.(interface{ Get(string) (interface{}, bool) }); ok {
		if mode, exists := ctx.Get(types.HoneypotModeKey); exists {
			if modeStr, ok := mode.(string); ok {
				return modeStr
			}
		}
	}
	return ""
}

// generateRealisticProxyKey 生成逼真的代理密钥
func (g *HoneypotDataGenerator) generateRealisticProxyKey(serviceType string) string {
	switch serviceType {
	case "openai":
		return g.generateRealisticKey("openai", 51)
	case "anthropic":
		return g.generateRealisticKey("anthropic", 108)
	case "groq":
		return g.generateRealisticKey("groq", 56)
	case "gemini":
		return g.generateRealisticKey("gemini", 39)
	case "azure":
		return g.generateRealisticKey("azure", 32)
	case "perplexity":
		return g.generateRealisticKey("perplexity", 56)
	default:
		return g.generateRealisticKey("openai", 51)
	}
}

// generateRealisticKey 生成逼真的密钥
func (g *HoneypotDataGenerator) generateRealisticKey(keyType string, length int) string {
	// 不同服务的真实密钥前缀
	prefixes := map[string][]string{
		"openai":     {"sk-", "sk-proj-"},
		"anthropic":  {"sk-ant-api03-"},
		"groq":       {"gsk_"},
		"gemini":     {"AIza", ""},
		"azure":      {"", ""}, // Azure 通常没有特殊前缀
		"perplexity": {"pplx-"},
		"qwen":       {"sk-"},
	}

	var prefix string
	if prefixList, exists := prefixes[keyType]; exists && len(prefixList) > 0 {
		// 随机选择一个前缀
		if len(prefixList) > 1 && g.rng.Float64() < 0.3 {
			prefix = prefixList[1] // 30%概率使用第二个前缀
		} else {
			prefix = prefixList[0]
		}
	}

	// 如果没有前缀或者是空前缀，使用通用的sk-格式
	if prefix == "" {
		prefix = "sk-"
	}

	// 生成真实长度的随机字符串
	remainingLength := length - len(prefix)
	if remainingLength <= 0 {
		remainingLength = 48 // 默认长度
	}

	// 使用真实的字符集：字母和数字
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	key := prefix
	for i := 0; i < remainingLength; i++ {
		key += string(charset[g.rng.Intn(len(charset))])
	}

	return key
}

// generateRealisticKeyWithRNG 使用指定的随机数生成器生成逼真的密钥
func (g *HoneypotDataGenerator) generateRealisticKeyWithRNG(keyType string, length int, rng *rand.Rand) string {
	// 不同服务的真实密钥前缀
	prefixes := map[string][]string{
		"openai":     {"sk-", "sk-proj-"},
		"anthropic":  {"sk-ant-api03-"},
		"groq":       {"gsk_"},
		"gemini":     {"AIza", ""},
		"azure":      {"", ""}, // Azure 通常没有特殊前缀
		"perplexity": {"pplx-"},
		"qwen":       {"sk-"},
	}

	var prefix string
	if prefixList, exists := prefixes[keyType]; exists && len(prefixList) > 0 {
		// 随机选择一个前缀
		if len(prefixList) > 1 && rng.Float64() < 0.3 {
			prefix = prefixList[1] // 30%概率使用第二个前缀
		} else {
			prefix = prefixList[0]
		}
	}

	// 如果没有前缀或者是空前缀，使用通用的sk-格式
	if prefix == "" {
		prefix = "sk-"
	}

	// 生成真实长度的随机字符串
	remainingLength := length - len(prefix)
	if remainingLength <= 0 {
		remainingLength = 48 // 默认长度
	}

	// 使用真实的字符集：字母和数字
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	key := prefix
	for i := 0; i < remainingLength; i++ {
		key += string(charset[rng.Intn(len(charset))])
	}

	return key
}

// getRealDomain 获取真实的域名
func (g *HoneypotDataGenerator) getRealDomain(serviceType string) string {
	domains := map[string]string{
		"openai":      "openai.com",
		"anthropic":   "anthropic.com",
		"claude":      "anthropic.com",
		"gemini":      "googleapis.com",
		"googleapis":  "googleapis.com",
		"azure":       "openai.azure.com",
		"azure.openai": "openai.azure.com",
		"groq":        "groq.com",
		"perplexity":  "perplexity.ai",
		"qwen":        "qwen.com",
		"deepseek":    "deepseek.com",
		"cohere":      "cohere.ai",
		"mistral":     "mistral.ai",
		"together":    "together.ai",
		"replicate":   "replicate.com",
		"huggingface": "huggingface.co",
	}

	if domain, exists := domains[serviceType]; exists {
		return domain
	}
	return serviceType + ".com"
}

// GenerateGroupStats 生成单个分组的统计数据
func (g *HoneypotDataGenerator) GenerateGroupStats(groupID uint) interface{} {
	// 为每个分组创建独立的随机数生成器，确保数据不同但一致
	groupRng := rand.New(rand.NewSource(g.seed + int64(groupID)*10000))

	// 生成分组密钥统计
	var totalKeys, activeKeys int64
	switch g.mode {
	case types.HoneypotModeDeceive:
		totalKeys = int64(groupRng.Intn(21) + 20) // 20-40个key
		activeKeys = int64(float64(totalKeys) * (0.8 + groupRng.Float64()*0.15)) // 80-95%活跃
	case types.HoneypotModeOverload:
		totalKeys = int64(groupRng.Intn(50000) + 50000) // 50000-99999个key
		activeKeys = int64(float64(totalKeys) * (0.85 + groupRng.Float64()*0.1)) // 85-95%活跃
	default:
		totalKeys = int64(groupRng.Intn(20) + 15) // 15-34个key，每个分组不同
		activeKeys = int64(float64(totalKeys) * (0.75 + groupRng.Float64()*0.2)) // 75-95%活跃
	}

	invalidKeys := totalKeys - activeKeys

	// 生成请求统计 - 每个分组都不同
	baseRequests := int64(groupRng.Intn(80000) + 20000) // 20k-100k基础请求
	totalRequests := baseRequests + int64(groupID)*1000 // 根据分组ID调整，确保不同
	successfulRequests := int64(float64(totalRequests) * (0.8 + groupRng.Float64()*0.15)) // 80-95%成功率
	failedRequests := totalRequests - successfulRequests

	// 生成RPM统计 - 每个分组都不同
	baseRPM := int64(groupRng.Intn(150) + 50) // 50-199基础RPM
	currentRPM := baseRPM + int64(groupID)*10 // 根据分组ID调整
	avgRPM := int64(float64(currentRPM) * (0.8 + groupRng.Float64()*0.4)) // 80-120%波动
	peakRPM := int64(float64(currentRPM) * (1.5 + groupRng.Float64()*0.5)) // 150-200%波动

	// 生成活跃密钥的详细信息
	activeKeysList := make([]map[string]interface{}, 0)
	displayCount := 10
	if totalKeys < 10 {
		displayCount = int(totalKeys)
	}

	for i := 0; i < displayCount; i++ {
		keyValue := g.generateRealisticKeyWithRNG("openai", 51, groupRng)
		// 根据分组ID调整密钥格式
		switch (groupID - 1) % 7 {
		case 0: // groq
			keyValue = g.generateRealisticKeyWithRNG("groq", 56, groupRng)
		case 1: // anthropic
			keyValue = g.generateRealisticKeyWithRNG("anthropic", 108, groupRng)
		case 2: // openai
			keyValue = g.generateRealisticKeyWithRNG("openai", 51, groupRng)
		case 3: // qwen
			keyValue = g.generateRealisticKeyWithRNG("qwen", 48, groupRng)
		case 4: // azure
			keyValue = g.generateRealisticKeyWithRNG("azure", 32, groupRng)
		case 5: // claude (anthropic)
			keyValue = g.generateRealisticKeyWithRNG("anthropic", 108, groupRng)
		case 6: // perplexity
			keyValue = g.generateRealisticKeyWithRNG("perplexity", 56, groupRng)
		}

		// 为每个密钥生成不同的统计数据
		requestCount := groupRng.Intn(8000) + 200 + int(groupID)*100 // 根据分组ID和密钥索引调整
		failureCount := groupRng.Intn(15) + int(groupID)%5

		activeKeysList = append(activeKeysList, map[string]interface{}{
			"id":            i + 1,
			"key_value":     keyValue,
			"key_hash":      generateKeyHash(keyValue),
			"status":        "active",
			"request_count": requestCount,
			"failure_count": failureCount,
			"last_used_at":  time.Now().Add(-time.Duration(groupRng.Intn(24)+int(groupID)) * time.Hour),
			"created_at":    time.Now().Add(-time.Duration(groupRng.Intn(30*24)+int(groupID)*24) * time.Hour),
			"updated_at":    time.Now().Add(-time.Duration(groupRng.Intn(7*24)+int(groupID)*6) * time.Hour),
		})
	}

	return map[string]interface{}{
		"key_stats": map[string]interface{}{
			"total_keys":   totalKeys,
			"active_keys":  activeKeys,
			"invalid_keys": invalidKeys,
		},
		"request_stats": map[string]interface{}{
			"total_requests":     totalRequests,
			"successful_requests": successfulRequests,
			"failed_requests":    failedRequests,
			"success_rate":       float64(successfulRequests) / float64(totalRequests) * 100,
		},
		"rpm_stats": map[string]interface{}{
			"current_rpm": currentRPM,
			"avg_rpm":     avgRPM,
			"peak_rpm":    peakRPM,
		},
		"active_keys": activeKeysList,
		"chart_data": g.generateChartDataForGroup(groupID),
	}
}

// generateChartData 生成图表数据
func (g *HoneypotDataGenerator) generateChartData() []map[string]interface{} {
	chartData := make([]map[string]interface{}, 24) // 24小时数据
	now := time.Now()

	for i := 0; i < 24; i++ {
		timestamp := now.Add(-time.Duration(23-i) * time.Hour)
		chartData[i] = map[string]interface{}{
			"timestamp":    timestamp.Format("2006-01-02 15:04:05"),
			"requests":     g.rng.Intn(1000) + 100,
			"errors":       g.rng.Intn(50) + 10,
			"avg_latency":  g.rng.Intn(500) + 100,
		}
	}

	return chartData
}

// generateChartDataForGroup 为特定分组生成图表数据
func (g *HoneypotDataGenerator) generateChartDataForGroup(groupID uint) []map[string]interface{} {
	chartData := make([]map[string]interface{}, 24) // 24小时数据
	now := time.Now()

	// 为每个分组创建独立的随机数生成器
	groupRng := rand.New(rand.NewSource(g.seed + int64(groupID)*5000))

	// 根据分组ID生成不同的基础值
	baseRequests := 100 + int(groupID)*20
	baseErrors := 10 + int(groupID)*2
	baseLatency := 100 + int(groupID)*15

	for i := 0; i < 24; i++ {
		timestamp := now.Add(-time.Duration(23-i) * time.Hour)

		// 为每个时间点生成不同但合理的数据
		requests := baseRequests + groupRng.Intn(500)
		errors := baseErrors + groupRng.Intn(30)
		avgLatency := baseLatency + groupRng.Intn(300)

		chartData[i] = map[string]interface{}{
			"timestamp":    timestamp.Format("2006-01-02 15:04:05"),
			"requests":     requests,
			"errors":       errors,
			"avg_latency":  avgLatency,
		}
	}

	return chartData
}
