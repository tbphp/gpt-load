package keypool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// 自动学习:根据上游真实反馈,将 (key, model) 标记为"已知不可服务"。
// 学习结果与配置文件 allowed_models 共同决定路由资格:
//   资格 = 配置允许(空=不限) AND 学习层未排除
// 学习层是软过滤:候选被学习层全部排除时回退到配置层候选,避免误杀整个模型。
//
// 记分模型(v2,指数退避):
//   - 连续 2 次 "Model access denied" 级拒绝 → 排除,基准 30 分钟
//   - 到期后再次拒绝 → 连续拒绝计数 +1,排除时长翻倍(上限 24 小时)
//   - 任何成功 → 连续拒绝计数清零(防御性:排除期内通常拿不到成功,
//     该路径主要覆盖"配置层强制放行"等边界)
//   - 学习状态进程内存保存;重启清零后重新学习(冷启动成本:2 次 403)

const (
	modelLearnBaseDuration    = 30 * time.Minute
	modelLearnMaxDuration     = 24 * time.Hour
	modelLearnStrikeThreshold = 2
)

type modelLearnEntry struct {
	strikes int       // 连续拒绝次数
	until   time.Time // 排除截止时间(零值=未排除)
}

// modelLearner 维护进程内 (groupID:keyID, model) 的学习状态。
// 单实例部署;集群模式下各节点独立学习,可接受。
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
// 达到阈值后排除;对永久无权限的 (key, model) 试探频率指数衰减。
func (l *modelLearner) recordModelDenied(groupID, keyID uint, model string) {
	if model == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	e.strikes++
	if e.strikes < modelLearnStrikeThreshold {
		return
	}
	d := modelLearnBaseDuration
	for i := 3; i <= e.strikes; i++ {
		d *= 2
		if d >= modelLearnMaxDuration {
			d = modelLearnMaxDuration
			break
		}
	}
	e.until = time.Now().Add(d)
}

// recordModelSuccess 记录一次成功服务:清零连续拒绝计数。
func (l *modelLearner) recordModelSuccess(groupID, keyID uint, model string) {
	if model == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	e.strikes = 0
	e.until = time.Time{}
}

// isModelExcluded 判断 (key, model) 当前是否被学习层排除。
// 排除到期后恢复参与(strikes 保留,再犯时升级时长)。
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
	if !ok || e.strikes < modelLearnStrikeThreshold {
		return false
	}
	if e.until.IsZero() {
		// 排除窗口已解除(过期或成功清零)
		return false
	}
	if !time.Now().Before(e.until) {
		// 刚过期:解除窗口,保留 strikes(再犯时翻倍升级)
		e.until = time.Time{}
		return false
	}
	return true
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

// strikesOf 返回 (key, model) 的连续拒绝计数(日志用)。
func (l *modelLearner) strikesOf(groupID, keyID uint, model string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	mm, ok := l.table[learnKey(groupID, keyID)]
	if !ok {
		return 0
	}
	return mm[model].strikes
}

// IsModelAccessDeniedError 判断上游错误是否属于"该 key 无此模型权限"类。
// 只匹配模型权限拒绝,不匹配 IP 白名单、配额、参数等其他 403。
func IsModelAccessDeniedError(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "model access denied") ||
		strings.Contains(lower, "access to model denied")
}

// setExcluded 测试辅助:直接设置排除状态
func (l *modelLearner) setExcluded(groupID, keyID uint, model string, excluded bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.getEntryLocked(groupID, keyID, model)
	if excluded {
		e.strikes = modelLearnStrikeThreshold
		e.until = time.Now().Add(modelLearnBaseDuration)
	} else {
		e.strikes = 0
		e.until = time.Time{}
	}
}
