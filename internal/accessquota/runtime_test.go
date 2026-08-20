package accessquota

import (
	"math"
	"testing"
	"time"
)

func TestRuntimeAppliesTotalAndActivityTriggeredPeriodicLimits(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 10, Revision: 1, Kind: KindTotal, LimitNanoUSD: 100},
		{ID: 11, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 20, PeriodSeconds: 5 * 60 * 60},
		{ID: 12, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 30, PeriodSeconds: 24 * 60 * 60},
	}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	started := time.Date(2026, time.August, 20, 10, 37, 0, 0, time.UTC)
	ticket, decision := runtime.Admit(1, started)
	if !decision.Allowed || len(ticket.Rules) != 3 {
		t.Fatalf("Admit() = ticket %#v decision %#v", ticket, decision)
	}
	runtime.Complete(ticket, 20)

	decision = runtime.Check(1, started.Add(time.Hour))
	if decision.Allowed || !decision.Recoverable || len(decision.BlockingRules) != 1 ||
		decision.BlockingRules[0].ID != 11 {
		t.Fatalf("Check(5h exhausted) = %#v", decision)
	}
	want5HEnd := started.Add(5 * time.Hour).UnixMilli()
	if decision.NextAvailableAtMS == nil || *decision.NextAvailableAtMS != want5HEnd {
		t.Fatalf("next available = %v, want %d", decision.NextAvailableAtMS, want5HEnd)
	}

	atBoundary := started.Add(5 * time.Hour)
	if decision = runtime.Check(1, atBoundary); !decision.Allowed {
		t.Fatalf("Check(at boundary) = %#v, want allowed", decision)
	}
	view := runtime.Snapshot(1, atBoundary)
	period5H := ruleViewByID(t, view.Rules, 11)
	if period5H.Status != RuleStatusInactive || period5H.UsedNanoUSD != 0 ||
		period5H.WindowStartedAtMS != nil || period5H.WindowEndsAtMS != nil {
		t.Fatalf("expired 5h view = %#v", period5H)
	}

	secondStart := time.Date(2026, time.August, 20, 18, 20, 0, 0, time.UTC)
	secondTicket, decision := runtime.Admit(1, secondStart)
	if !decision.Allowed {
		t.Fatalf("second Admit() decision = %#v", decision)
	}
	runtime.Complete(secondTicket, 10)
	decision = runtime.Check(1, secondStart.Add(time.Minute))
	if decision.Allowed || len(decision.BlockingRules) != 1 || decision.BlockingRules[0].ID != 12 {
		t.Fatalf("Check(24h exhausted) = %#v", decision)
	}
	view = runtime.Snapshot(1, secondStart.Add(time.Minute))
	if total := ruleViewByID(t, view.Rules, 10); total.UsedNanoUSD != 30 {
		t.Fatalf("total used = %d, want 30", total.UsedNanoUSD)
	}
	if period := ruleViewByID(t, view.Rules, 11); period.UsedNanoUSD != 10 {
		t.Fatalf("new 5h used = %d, want 10", period.UsedNanoUSD)
	}
}

func TestRuntimeReturnsEveryBlockingRuleAndLatestPeriodicRecovery(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 10, Revision: 1, Kind: KindTotal, LimitNanoUSD: 100},
		{ID: 11, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 20, PeriodSeconds: 5 * 60 * 60},
		{ID: 12, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 30, PeriodSeconds: 24 * 60 * 60},
	}}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	ticket, _ := runtime.Admit(1, start)
	runtime.Complete(ticket, 30)
	decision := runtime.Check(1, start.Add(time.Minute))
	if decision.Allowed || !decision.Recoverable || len(decision.BlockingRules) != 2 ||
		decision.BlockingRules[0].ID != 11 || decision.BlockingRules[1].ID != 12 ||
		decision.NextAvailableAtMS == nil ||
		*decision.NextAvailableAtMS != start.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("periodic blockers = %#v", decision)
	}

	runtime.Complete(ticket, 70)
	decision = runtime.Check(1, start.Add(time.Minute))
	if decision.Allowed || decision.Recoverable || decision.NextAvailableAtMS != nil ||
		len(decision.BlockingRules) != 3 || decision.BlockingRules[0].ID != 10 {
		t.Fatalf("total and periodic blockers = %#v", decision)
	}
}

func TestRuntimePreservesExpiredGenerationUntilNextAdmit(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 20, Revision: 1, Kind: KindTotal, LimitNanoUSD: 100},
		{ID: 21, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 20, PeriodSeconds: 60},
	}}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	oldTicket, _ := runtime.Admit(1, start)
	if decision := runtime.Check(1, start.Add(time.Minute)); !decision.Allowed {
		t.Fatalf("expired Check() = %#v", decision)
	}
	runtime.Complete(oldTicket, 7)

	newTicket, decision := runtime.Admit(1, start.Add(2*time.Minute))
	if !decision.Allowed {
		t.Fatalf("new Admit() = %#v", decision)
	}
	runtime.Complete(oldTicket, 5)
	runtime.Complete(newTicket, 3)

	view := runtime.Snapshot(1, start.Add(2*time.Minute))
	if total := ruleViewByID(t, view.Rules, 20); total.UsedNanoUSD != 15 {
		t.Fatalf("total used = %d, want 15", total.UsedNanoUSD)
	}
	periodic := ruleViewByID(t, view.Rules, 21)
	if periodic.UsedNanoUSD != 3 || periodic.WindowGeneration != 2 {
		t.Fatalf("new periodic state = %#v, want used 3 generation 2", periodic)
	}
}

func TestRuntimeReconcilePreservesAmountStateAndInvalidatesNewRevisionTickets(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 30, Revision: 1, Kind: KindTotal, LimitNanoUSD: 100},
		{ID: 31, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 50, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 20, 11, 0, 0, 0, time.UTC)
	oldTicket, _ := runtime.Admit(1, now)
	runtime.Complete(oldTicket, 40)

	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 30, Revision: 1, Kind: KindTotal, LimitNanoUSD: 35},
		{ID: 31, Revision: 2, Kind: KindPeriodic, LimitNanoUSD: 50, PeriodSeconds: 600},
	}}); err != nil {
		t.Fatal(err)
	}
	if decision := runtime.Check(1, now); decision.Allowed || decision.BlockingRules[0].ID != 30 {
		t.Fatalf("Check(after amount decrease) = %#v", decision)
	}
	runtime.Complete(oldTicket, 5)
	view := runtime.Snapshot(1, now)
	if total := ruleViewByID(t, view.Rules, 30); total.UsedNanoUSD != 45 {
		t.Fatalf("total used = %d, want 45", total.UsedNanoUSD)
	}
	if periodic := ruleViewByID(t, view.Rules, 31); periodic.UsedNanoUSD != 0 || periodic.Status != RuleStatusInactive {
		t.Fatalf("new periodic revision = %#v", periodic)
	}
}

func TestRuntimeRestoreRequiresExactRuleStateAndKeepsActiveWindow(t *testing.T) {
	start := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	runtime := NewRuntime()
	if err := runtime.Restore([]RestoredState{{
		AccessKeyID: 1, RuleID: 40, RuleRevision: 1, UsedNanoUSD: 7,
		WindowStartedAtMS: ptrInt64(start.UnixMilli()), WindowEndsAtMS: ptrInt64(end.UnixMilli()),
		WindowGeneration: 3, SnapshotVersion: 9,
	}}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if err := runtime.Reconcile(map[uint][]Rule{1: {{
		ID: 40, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 10, PeriodSeconds: 3600,
	}}}); err != nil {
		t.Fatalf("Reconcile(restored) error = %v", err)
	}
	view := runtime.Snapshot(1, start.Add(time.Minute))
	periodic := ruleViewByID(t, view.Rules, 40)
	if periodic.UsedNanoUSD != 7 || periodic.WindowGeneration != 3 || periodic.WindowEndsAtMS == nil ||
		*periodic.WindowEndsAtMS != end.UnixMilli() {
		t.Fatalf("restored periodic = %#v", periodic)
	}

	missing := NewRuntime()
	if err := missing.Restore(nil); err != nil {
		t.Fatal(err)
	}
	if err := missing.Reconcile(map[uint][]Rule{1: {{
		ID: 41, Revision: 1, Kind: KindTotal, LimitNanoUSD: 10,
	}}}); err == nil {
		t.Fatal("Reconcile(missing state) error = nil")
	}

	orphan := NewRuntime()
	if err := orphan.Restore([]RestoredState{{
		AccessKeyID: 1, RuleID: 99, RuleRevision: 1, SnapshotVersion: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := orphan.Reconcile(nil); err == nil {
		t.Fatal("Reconcile(orphan state) error = nil")
	}
}

func TestRuntimeDirtySnapshotsAckOnlyPersistedVersion(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {{
		ID: 50, Revision: 1, Kind: KindTotal, LimitNanoUSD: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	ticket, _ := runtime.Admit(1, time.Now())
	runtime.Complete(ticket, 10)
	first := runtime.DirtySnapshots(10)
	if len(first) != 1 || first[0].UsedNanoUSD != 10 {
		t.Fatalf("DirtySnapshots() = %#v", first)
	}
	runtime.Complete(ticket, 5)
	runtime.Ack(1, 50, 1, first[0].SnapshotVersion)
	second := runtime.DirtySnapshots(10)
	if len(second) != 1 || second[0].UsedNanoUSD != 15 ||
		second[0].SnapshotVersion <= first[0].SnapshotVersion {
		t.Fatalf("dirty after old Ack = %#v", second)
	}
	runtime.Ack(1, 50, 1, second[0].SnapshotVersion)
	if got := runtime.DirtySnapshots(10); len(got) != 0 {
		t.Fatalf("dirty after current Ack = %#v", got)
	}
}

func TestRuntimeNotifiesCheckpointWorkerAfterDirtyStateChanges(t *testing.T) {
	runtime := NewRuntime()
	if err := runtime.Reconcile(map[uint][]Rule{1: {
		{ID: 11, Revision: 1, Kind: KindPeriodic, LimitNanoUSD: 100, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	notifications := 0
	runtime.SetDirtyNotifier(func() { notifications++ })
	ticket, decision := runtime.Admit(1, time.Unix(100, 0))
	if !decision.Allowed {
		t.Fatalf("Admit() decision = %#v", decision)
	}
	runtime.Complete(ticket, 1)
	if notifications != 2 {
		t.Fatalf("checkpoint notifications = %d, want 2", notifications)
	}
}

func TestRuntimeSaturatesInvalidAndOverflowingCosts(t *testing.T) {
	for _, test := range []struct {
		cost      int64
		wantFault CompletionFault
	}{
		{cost: -1, wantFault: CompletionFaultNegativeEstimate},
		{cost: 10, wantFault: CompletionFaultOverflow},
	} {
		t.Run(time.Duration(test.cost).String(), func(t *testing.T) {
			runtime := NewRuntime()
			if err := runtime.Reconcile(map[uint][]Rule{1: {{
				ID: 60, Revision: 1, Kind: KindTotal, LimitNanoUSD: math.MaxInt64,
			}}}); err != nil {
				t.Fatal(err)
			}
			ticket, _ := runtime.Admit(1, time.Now())
			if test.cost > 0 {
				runtime.Complete(ticket, math.MaxInt64-5)
			}
			completion := runtime.Complete(ticket, test.cost)
			if completion.Fault != test.wantFault {
				t.Fatalf("Complete() = %#v, want fault %q", completion, test.wantFault)
			}
			view := runtime.Snapshot(1, time.Now())
			if got := ruleViewByID(t, view.Rules, 60).UsedNanoUSD; got != math.MaxInt64 {
				t.Fatalf("used = %d, want MaxInt64", got)
			}
			if decision := runtime.Check(1, time.Now()); decision.Allowed {
				t.Fatalf("Check() = %#v, want blocked", decision)
			}
			if got := runtime.Stats().OverflowFaultTotal; got != 1 {
				t.Fatalf("OverflowFaultTotal = %d, want 1", got)
			}
		})
	}
}

func ruleViewByID(t *testing.T, rules []RuleView, id uint) RuleView {
	t.Helper()
	for _, rule := range rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("rule %d not found in %#v", id, rules)
	return RuleView{}
}

func ptrInt64(value int64) *int64 { return &value }
