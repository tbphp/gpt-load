package keypool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// 自动学习:根据上游真实反馈，将 (key, model) 标记为"已知不可服务"。
// 学习结果与配置文件 allowed_models 共同决定路由资格:
//   资格 = 配置允许(空=不限) AND 学习层未排除
// 学习层是软过滤:候选被学习层全部排除时回退到配置层候选，避免误杀整个模型。

const (
	// modelLearnDenyScore 一次模型权限拒绝的扣分
	modelLearnDenyScore = -2
	// modelLearnSuccessScore 一次成功请求的加分
	modelLearnSuccessScore = 1
	// modelLearnExcludeThreshold 分数低于等于该值则排除
	modelLearnExcludeThreshold = -3
	// modelLearnExcludeDuration 排除持续时间
	modelLearnExcludeDuration = 30 * time.Minute
)

type modelLearnEntry struct {
	score int
	until time.Time // 空值表示未排除
}

// modelLearner 维护进程内 (groupID:keyID, model) 的学习状态。
// 单实例部署；集群模式下各节点独立学习，可接受。
type modelLearner struct {
	mu    sync.Mutex
	table map[string]map[string]*modelLearnEntry
}

func newModelLearner() *modelLearner {
	return &modelLearner{table: make(map[string]map[string]*modelLearnEntry)}
}

func learnKey(groupID, keyID uint) string {
	return fmt.Sprintf("%d:%d", groupID, keyID)
}

// recordModelDenied 记录一次 "Model access denied" 类的上游拒绝。
func (l *modelLearner) recordModelDenied(groupID, keyID uint, model string) {
	if model == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	e.score += modelLearnDenyScore
	if e.score <= modelLearnExcludeThreshold {
		e.until = time.Now().Add(modelLearnExcludeDuration)
	}
}

// recordModelSuccess 记录一次成功服务。
func (l *modelLearner) recordModelSuccess(groupID, keyID uint, model string) {
	if model == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	e.score += modelLearnSuccessScore
	if e.score > modelLearnExcludeThreshold {
		e.until = time.Time{}
	}
}

// isModelExcluded 判断 (key, model) 当前是否被学习层排除；
// 排除已过期时自动恢复并清零分数。
func (l *modelLearner) isModelExcluded(groupID, keyID uint, model string) bool {
	if model == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	mm, ok := l.table[learnKey(groupID, keyID)]
	if !ok {
		return false
	}
	e, ok := mm[model]
	if !ok {
		return false
	}
	if !e.until.IsZero() && time.Now().After(e.until) {
		// 排除过期，自动恢复
		e.score = 0
		e.until = time.Time{}
		return false
	}
	return e.score <= modelLearnExcludeThreshold
}

func (l *modelLearner) getEntryLocked(groupID, keyID uint, model string) *modelLearnEntry {
	k := learnKey(groupID, keyID)
	mm, ok := l.table[k]
	if !ok {
		mm = make(map[string]*modelLearnEntry)
		l.table[k] = mm
	}
	e, ok := mm[model]
	if !ok {
		e = &modelLearnEntry{}
		mm[model] = e
	}
	return e
}

// IsModelAccessDeniedError 判断上游错误是否属于"该 key 无此模型权限"类。
// 只匹配模型权限拒绝，不匹配 IP 白名单、配额、参数等其他 403。
func IsModelAccessDeniedError(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "model access denied") ||
		strings.Contains(lower, "access to model denied")
}

// setExcluded 测试辅助：直接设置排除状态
func (l *modelLearner) setExcluded(groupID, keyID uint, model string, excluded bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	if excluded {
		e.score = modelLearnExcludeThreshold
		e.until = time.Now().Add(modelLearnExcludeDuration)
	} else {
		e.score = 0
		e.until = time.Time{}
	}
}