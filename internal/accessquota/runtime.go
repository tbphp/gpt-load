// Package accessquota owns AccessKey estimated-cost limit runtime state.
package accessquota

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MinPeriodSeconds int64 = 60
	MaxPeriodSeconds int64 = 365 * 24 * 60 * 60
	MaxPeriodicRules       = 10
)

type Kind string

const (
	KindTotal    Kind = "total"
	KindPeriodic Kind = "periodic"
)

type Rule struct {
	ID            uint
	Revision      uint64
	Kind          Kind
	LimitNanoUSD  int64
	PeriodSeconds int64
}

type RestoredState struct {
	AccessKeyID       uint
	RuleID            uint
	RuleRevision      uint64
	UsedNanoUSD       int64
	WindowStartedAtMS *int64
	WindowEndsAtMS    *int64
	WindowGeneration  uint64
	SnapshotVersion   uint64
}

type TicketRule struct {
	RuleID           uint
	RuleRevision     uint64
	WindowGeneration uint64
}

type Ticket struct {
	AccessKeyID uint
	Rules       []TicketRule
}

type RuleStatus string

const (
	RuleStatusAvailable RuleStatus = "available"
	RuleStatusInactive  RuleStatus = "inactive"
	RuleStatusExhausted RuleStatus = "exhausted"
)

type RuleView struct {
	Rule
	UsedNanoUSD       int64
	RemainingNanoUSD  int64
	Status            RuleStatus
	WindowStartedAtMS *int64
	WindowEndsAtMS    *int64
	WindowGeneration  uint64
}

type Decision struct {
	Allowed           bool
	Recoverable       bool
	NextAvailableAtMS *int64
	BlockingRules     []RuleView
}

type View struct {
	ObservedAtMS      int64
	Allowed           bool
	Recoverable       bool
	NextAvailableAtMS *int64
	Rules             []RuleView
}

type Stats struct {
	OverflowFaultTotal uint64
}

type CompletionFault string

const (
	CompletionFaultNegativeEstimate CompletionFault = "negative_estimate"
	CompletionFaultOverflow         CompletionFault = "overflow"
)

type CompletionResult struct {
	Fault CompletionFault
}

type Runtime struct {
	mu             sync.RWMutex
	entries        map[uint]*accessKeyEntry
	restored       map[uint]RestoredState
	restorePending bool
	dirtyNotifier  func()

	overflowFaultTotal atomic.Uint64
}

type accessKeyEntry struct {
	mu    sync.Mutex
	rules []*runtimeRule
	byID  map[uint]*runtimeRule
}

type runtimeRule struct {
	definition        Rule
	usedNanoUSD       int64
	windowStartedAtMS *int64
	windowEndsAtMS    *int64
	windowGeneration  uint64
	snapshotVersion   uint64
	dirty             bool
}

func NewRuntime() *Runtime {
	return &Runtime{entries: make(map[uint]*accessKeyEntry)}
}

// SetDirtyNotifier installs the process-owned non-blocking checkpoint wake-up.
func (runtime *Runtime) SetDirtyNotifier(notifier func()) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.dirtyNotifier = notifier
	runtime.mu.Unlock()
}

// ValidateDefinitions verifies all per-key rule identities and limits.
func ValidateDefinitions(definitions map[uint][]Rule) error {
	_, err := normalizeDefinitions(definitions)
	return err
}

func (runtime *Runtime) Restore(states []RestoredState) error {
	if runtime == nil {
		return fmt.Errorf("restore access quota runtime: runtime is nil")
	}
	restored := make(map[uint]RestoredState, len(states))
	for _, state := range states {
		if err := validateRestoredState(state); err != nil {
			return err
		}
		if _, exists := restored[state.RuleID]; exists {
			return fmt.Errorf("restore access quota rule %d: duplicate state", state.RuleID)
		}
		restored[state.RuleID] = cloneRestoredState(state)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.restorePending || len(runtime.entries) != 0 {
		return fmt.Errorf("restore access quota runtime: runtime is already initialized")
	}
	runtime.restored = restored
	runtime.restorePending = true
	return nil
}

func (runtime *Runtime) Reconcile(definitions map[uint][]Rule) error {
	if runtime == nil {
		return fmt.Errorf("reconcile access quota runtime: runtime is nil")
	}
	normalized, err := normalizeDefinitions(definitions)
	if err != nil {
		return err
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.entries == nil {
		runtime.entries = make(map[uint]*accessKeyEntry)
	}
	if runtime.restorePending {
		entries, err := entriesFromRestore(normalized, runtime.restored)
		if err != nil {
			return err
		}
		runtime.entries = entries
		runtime.restored = nil
		runtime.restorePending = false
		return nil
	}

	next := make(map[uint]*accessKeyEntry, len(normalized))
	for accessKeyID, rules := range normalized {
		if len(rules) == 0 {
			continue
		}
		entry := newAccessKeyEntry(rules)
		if current := runtime.entries[accessKeyID]; current != nil {
			current.mu.Lock()
			for _, rule := range entry.rules {
				previous := current.byID[rule.definition.ID]
				if previous == nil || previous.definition.Revision != rule.definition.Revision {
					continue
				}
				if previous.definition.Kind != rule.definition.Kind ||
					previous.definition.PeriodSeconds != rule.definition.PeriodSeconds {
					current.mu.Unlock()
					return fmt.Errorf(
						"reconcile access quota rule %d: kind or period changed without revision",
						rule.definition.ID,
					)
				}
				cloneRuntimeState(rule, previous)
			}
			current.mu.Unlock()
		}
		next[accessKeyID] = entry
	}
	runtime.entries = next
	return nil
}

func (runtime *Runtime) Check(accessKeyID uint, now time.Time) Decision {
	entry := runtime.entry(accessKeyID)
	if entry == nil {
		return allowedDecision()
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return decisionLocked(entry, now.UnixMilli())
}

func (runtime *Runtime) Admit(accessKeyID uint, now time.Time) (Ticket, Decision) {
	entry := runtime.entry(accessKeyID)
	if entry == nil {
		return Ticket{AccessKeyID: accessKeyID}, allowedDecision()
	}
	nowMS := now.UnixMilli()
	entry.mu.Lock()
	decision := decisionLocked(entry, nowMS)
	if !decision.Allowed {
		entry.mu.Unlock()
		return Ticket{AccessKeyID: accessKeyID}, decision
	}

	dirty := false
	for _, rule := range entry.rules {
		if rule.definition.Kind != KindPeriodic || !periodicInactive(rule, nowMS) {
			continue
		}
		startedAt := nowMS
		endsAt := now.Add(time.Duration(rule.definition.PeriodSeconds) * time.Second).UnixMilli()
		rule.windowStartedAtMS = &startedAt
		rule.windowEndsAtMS = &endsAt
		rule.usedNanoUSD = 0
		if rule.windowGeneration < math.MaxUint64 {
			rule.windowGeneration++
		}
		advanceVersion(rule)
		dirty = true
	}

	ticket := Ticket{AccessKeyID: accessKeyID, Rules: make([]TicketRule, 0, len(entry.rules))}
	for _, rule := range entry.rules {
		ticket.Rules = append(ticket.Rules, TicketRule{
			RuleID: rule.definition.ID, RuleRevision: rule.definition.Revision,
			WindowGeneration: rule.windowGeneration,
		})
	}
	entry.mu.Unlock()
	if dirty {
		runtime.notifyDirty()
	}
	return ticket, allowedDecision()
}

func (runtime *Runtime) Complete(ticket Ticket, costNanoUSD int64) CompletionResult {
	if runtime == nil || ticket.AccessKeyID == 0 || len(ticket.Rules) == 0 {
		return CompletionResult{}
	}
	entry := runtime.entry(ticket.AccessKeyID)
	if entry == nil {
		return CompletionResult{}
	}
	entry.mu.Lock()

	completion := CompletionResult{}
	dirty := false
	for _, ticketRule := range ticket.Rules {
		rule := entry.byID[ticketRule.RuleID]
		if rule == nil || rule.definition.Revision != ticketRule.RuleRevision {
			continue
		}
		if rule.definition.Kind == KindPeriodic && rule.windowGeneration != ticketRule.WindowGeneration {
			continue
		}
		used := rule.usedNanoUSD
		switch {
		case costNanoUSD < 0:
			used = math.MaxInt64
			completion.Fault = CompletionFaultNegativeEstimate
		case costNanoUSD == 0:
			continue
		case used > math.MaxInt64-costNanoUSD:
			used = math.MaxInt64
			if completion.Fault == "" {
				completion.Fault = CompletionFaultOverflow
			}
		default:
			used += costNanoUSD
		}
		if used == rule.usedNanoUSD {
			continue
		}
		rule.usedNanoUSD = used
		advanceVersion(rule)
		dirty = true
	}
	entry.mu.Unlock()
	if completion.Fault != "" {
		runtime.overflowFaultTotal.Add(1)
	}
	if dirty {
		runtime.notifyDirty()
	}
	return completion
}

func (runtime *Runtime) Snapshot(accessKeyID uint, now time.Time) View {
	view := View{ObservedAtMS: now.UnixMilli(), Allowed: true, Recoverable: true, Rules: []RuleView{}}
	entry := runtime.entry(accessKeyID)
	if entry == nil {
		return view
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	view.Rules = make([]RuleView, 0, len(entry.rules))
	for _, rule := range entry.rules {
		view.Rules = append(view.Rules, ruleViewLocked(rule, view.ObservedAtMS))
	}
	decision := decisionLocked(entry, view.ObservedAtMS)
	view.Allowed = decision.Allowed
	view.Recoverable = decision.Recoverable
	view.NextAvailableAtMS = cloneInt64(decision.NextAvailableAtMS)
	return view
}

func (runtime *Runtime) DirtySnapshots(limit int) []RestoredState {
	if runtime == nil || limit == 0 {
		return nil
	}
	runtime.mu.RLock()
	ids := make([]uint, 0, len(runtime.entries))
	for id := range runtime.entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	entries := make([]*accessKeyEntry, len(ids))
	for index, id := range ids {
		entries[index] = runtime.entries[id]
	}
	runtime.mu.RUnlock()

	result := make([]RestoredState, 0)
	for index, entry := range entries {
		entry.mu.Lock()
		for _, rule := range entry.rules {
			if !rule.dirty {
				continue
			}
			result = append(result, restoredState(ids[index], rule))
			if limit > 0 && len(result) >= limit {
				entry.mu.Unlock()
				return result
			}
		}
		entry.mu.Unlock()
	}
	return result
}

func (runtime *Runtime) Ack(accessKeyID, ruleID uint, revision, snapshotVersion uint64) {
	entry := runtime.entry(accessKeyID)
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	rule := entry.byID[ruleID]
	if rule == nil || rule.definition.Revision != revision || rule.snapshotVersion != snapshotVersion {
		return
	}
	rule.dirty = false
}

func (runtime *Runtime) Stats() Stats {
	if runtime == nil {
		return Stats{}
	}
	return Stats{OverflowFaultTotal: runtime.overflowFaultTotal.Load()}
}

func (runtime *Runtime) notifyDirty() {
	if runtime == nil {
		return
	}
	runtime.mu.RLock()
	notifier := runtime.dirtyNotifier
	runtime.mu.RUnlock()
	if notifier != nil {
		notifier()
	}
}

func (runtime *Runtime) entry(accessKeyID uint) *accessKeyEntry {
	if runtime == nil || accessKeyID == 0 {
		return nil
	}
	runtime.mu.RLock()
	entry := runtime.entries[accessKeyID]
	runtime.mu.RUnlock()
	return entry
}

func normalizeDefinitions(definitions map[uint][]Rule) (map[uint][]Rule, error) {
	normalized := make(map[uint][]Rule, len(definitions))
	globalRuleIDs := make(map[uint]uint)
	for accessKeyID, source := range definitions {
		if accessKeyID == 0 {
			return nil, fmt.Errorf("reconcile access quota runtime: access key ID is required")
		}
		if len(source) == 0 {
			continue
		}
		rules := append([]Rule(nil), source...)
		if err := validateRules(accessKeyID, rules, globalRuleIDs); err != nil {
			return nil, err
		}
		sortRules(rules)
		normalized[accessKeyID] = rules
	}
	return normalized, nil
}

func validateRules(accessKeyID uint, rules []Rule, globalRuleIDs map[uint]uint) error {
	totalCount := 0
	periodicCount := 0
	periods := make(map[int64]struct{})
	for _, rule := range rules {
		if rule.ID == 0 || rule.Revision == 0 || rule.LimitNanoUSD <= 0 {
			return fmt.Errorf("reconcile access quota rule for key %d: invalid identity, revision, or limit", accessKeyID)
		}
		if owner, exists := globalRuleIDs[rule.ID]; exists {
			return fmt.Errorf("reconcile access quota rule %d: duplicate across keys %d and %d", rule.ID, owner, accessKeyID)
		}
		globalRuleIDs[rule.ID] = accessKeyID
		switch rule.Kind {
		case KindTotal:
			totalCount++
			if rule.PeriodSeconds != 0 {
				return fmt.Errorf("reconcile total access quota rule %d: period must be zero", rule.ID)
			}
		case KindPeriodic:
			periodicCount++
			if rule.PeriodSeconds < MinPeriodSeconds || rule.PeriodSeconds > MaxPeriodSeconds {
				return fmt.Errorf("reconcile periodic access quota rule %d: period is out of range", rule.ID)
			}
			if _, exists := periods[rule.PeriodSeconds]; exists {
				return fmt.Errorf("reconcile periodic access quota rule %d: duplicate period", rule.ID)
			}
			periods[rule.PeriodSeconds] = struct{}{}
		default:
			return fmt.Errorf("reconcile access quota rule %d: invalid kind %q", rule.ID, rule.Kind)
		}
	}
	if totalCount > 1 || periodicCount > MaxPeriodicRules {
		return fmt.Errorf("reconcile access quota rules for key %d: rule count exceeds limit", accessKeyID)
	}
	return nil
}

func sortRules(rules []Rule) {
	sort.Slice(rules, func(i, j int) bool {
		left, right := rules[i], rules[j]
		if left.Kind != right.Kind {
			return left.Kind == KindTotal
		}
		if left.PeriodSeconds != right.PeriodSeconds {
			return left.PeriodSeconds < right.PeriodSeconds
		}
		return left.ID < right.ID
	})
}

func entriesFromRestore(
	definitions map[uint][]Rule,
	restored map[uint]RestoredState,
) (map[uint]*accessKeyEntry, error) {
	remaining := make(map[uint]RestoredState, len(restored))
	for id, state := range restored {
		remaining[id] = state
	}
	entries := make(map[uint]*accessKeyEntry, len(definitions))
	for accessKeyID, rules := range definitions {
		entry := newAccessKeyEntry(rules)
		for _, rule := range entry.rules {
			state, exists := remaining[rule.definition.ID]
			if !exists {
				return nil, fmt.Errorf("restore access quota rule %d for key %d: state is missing", rule.definition.ID, accessKeyID)
			}
			if state.AccessKeyID != accessKeyID || state.RuleRevision != rule.definition.Revision {
				return nil, fmt.Errorf("restore access quota rule %d: state identity or revision mismatch", rule.definition.ID)
			}
			if err := applyRestoredState(rule, state); err != nil {
				return nil, err
			}
			delete(remaining, rule.definition.ID)
		}
		entries[accessKeyID] = entry
	}
	if len(remaining) != 0 {
		ids := make([]uint, 0, len(remaining))
		for id := range remaining {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		return nil, fmt.Errorf("restore access quota runtime: orphan state for rule %d", ids[0])
	}
	return entries, nil
}

func newAccessKeyEntry(rules []Rule) *accessKeyEntry {
	entry := &accessKeyEntry{rules: make([]*runtimeRule, 0, len(rules)), byID: make(map[uint]*runtimeRule, len(rules))}
	for _, definition := range rules {
		rule := &runtimeRule{definition: definition, snapshotVersion: 1}
		entry.rules = append(entry.rules, rule)
		entry.byID[definition.ID] = rule
	}
	return entry
}

func validateRestoredState(state RestoredState) error {
	if state.AccessKeyID == 0 || state.RuleID == 0 || state.RuleRevision == 0 ||
		state.UsedNanoUSD < 0 || state.SnapshotVersion == 0 {
		return fmt.Errorf("restore access quota rule %d: invalid persisted state", state.RuleID)
	}
	if (state.WindowStartedAtMS == nil) != (state.WindowEndsAtMS == nil) {
		return fmt.Errorf("restore access quota rule %d: incomplete window", state.RuleID)
	}
	if state.WindowStartedAtMS != nil &&
		(*state.WindowStartedAtMS < 0 || *state.WindowEndsAtMS <= *state.WindowStartedAtMS) {
		return fmt.Errorf("restore access quota rule %d: invalid window", state.RuleID)
	}
	return nil
}

func applyRestoredState(rule *runtimeRule, state RestoredState) error {
	if rule.definition.Kind == KindTotal {
		if state.WindowStartedAtMS != nil || state.WindowEndsAtMS != nil || state.WindowGeneration != 0 {
			return fmt.Errorf("restore total access quota rule %d: window state is not allowed", rule.definition.ID)
		}
	} else if state.WindowStartedAtMS == nil && state.WindowGeneration != 0 {
		return fmt.Errorf("restore periodic access quota rule %d: inactive state has generation", rule.definition.ID)
	} else if state.WindowStartedAtMS != nil && state.WindowGeneration == 0 {
		return fmt.Errorf("restore periodic access quota rule %d: active state has no generation", rule.definition.ID)
	}
	rule.usedNanoUSD = state.UsedNanoUSD
	rule.windowStartedAtMS = cloneInt64(state.WindowStartedAtMS)
	rule.windowEndsAtMS = cloneInt64(state.WindowEndsAtMS)
	rule.windowGeneration = state.WindowGeneration
	rule.snapshotVersion = state.SnapshotVersion
	return nil
}

func cloneRuntimeState(target, source *runtimeRule) {
	target.usedNanoUSD = source.usedNanoUSD
	target.windowStartedAtMS = cloneInt64(source.windowStartedAtMS)
	target.windowEndsAtMS = cloneInt64(source.windowEndsAtMS)
	target.windowGeneration = source.windowGeneration
	target.snapshotVersion = source.snapshotVersion
	target.dirty = source.dirty
}

func decisionLocked(entry *accessKeyEntry, nowMS int64) Decision {
	decision := allowedDecision()
	var next int64
	for _, rule := range entry.rules {
		view := ruleViewLocked(rule, nowMS)
		if view.Status != RuleStatusExhausted {
			continue
		}
		decision.Allowed = false
		decision.BlockingRules = append(decision.BlockingRules, view)
		if rule.definition.Kind == KindTotal {
			decision.Recoverable = false
			decision.NextAvailableAtMS = nil
			continue
		}
		if decision.Recoverable && view.WindowEndsAtMS != nil && *view.WindowEndsAtMS > next {
			next = *view.WindowEndsAtMS
		}
	}
	if !decision.Allowed && decision.Recoverable && next > 0 {
		decision.NextAvailableAtMS = &next
	}
	return decision
}

func allowedDecision() Decision {
	return Decision{Allowed: true, Recoverable: true, BlockingRules: []RuleView{}}
}

func ruleViewLocked(rule *runtimeRule, nowMS int64) RuleView {
	view := RuleView{
		Rule: rule.definition, RemainingNanoUSD: rule.definition.LimitNanoUSD,
		Status: RuleStatusAvailable, WindowGeneration: rule.windowGeneration,
	}
	if rule.definition.Kind == KindPeriodic && periodicInactive(rule, nowMS) {
		view.Status = RuleStatusInactive
		return view
	}
	view.UsedNanoUSD = rule.usedNanoUSD
	view.RemainingNanoUSD = remaining(rule.definition.LimitNanoUSD, rule.usedNanoUSD)
	view.WindowStartedAtMS = cloneInt64(rule.windowStartedAtMS)
	view.WindowEndsAtMS = cloneInt64(rule.windowEndsAtMS)
	if rule.usedNanoUSD >= rule.definition.LimitNanoUSD {
		view.Status = RuleStatusExhausted
	}
	return view
}

func periodicInactive(rule *runtimeRule, nowMS int64) bool {
	return rule.windowStartedAtMS == nil || rule.windowEndsAtMS == nil || nowMS >= *rule.windowEndsAtMS
}

func remaining(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}

func advanceVersion(rule *runtimeRule) {
	if rule.snapshotVersion < math.MaxUint64 {
		rule.snapshotVersion++
	}
	rule.dirty = true
}

func restoredState(accessKeyID uint, rule *runtimeRule) RestoredState {
	return RestoredState{
		AccessKeyID: accessKeyID, RuleID: rule.definition.ID, RuleRevision: rule.definition.Revision,
		UsedNanoUSD: rule.usedNanoUSD, WindowStartedAtMS: cloneInt64(rule.windowStartedAtMS),
		WindowEndsAtMS: cloneInt64(rule.windowEndsAtMS), WindowGeneration: rule.windowGeneration,
		SnapshotVersion: rule.snapshotVersion,
	}
}

func cloneRestoredState(state RestoredState) RestoredState {
	state.WindowStartedAtMS = cloneInt64(state.WindowStartedAtMS)
	state.WindowEndsAtMS = cloneInt64(state.WindowEndsAtMS)
	return state
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
