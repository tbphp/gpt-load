package control

import (
	"testing"
	"time"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestKiroRediscoverFinishNoLocalAccount(t *testing.T) {
	var result KiroRediscoverResult
	result.PreviousAccount = "account-a"
	if !result.finish(subscriptionruntime.Credential{}, false) {
		t.Fatal("expected finish for missing local account")
	}
	if result.Reason != "no_local_account" {
		t.Fatalf("reason = %q, want no_local_account", result.Reason)
	}
	if result.CurrentAccount != "account-a" {
		t.Fatalf("current = %q, want unchanged account-a", result.CurrentAccount)
	}
}

func TestKiroRediscoverFinishSameAccount(t *testing.T) {
	var result KiroRediscoverResult
	result.PreviousAccount = "account-a"
	fresh := subscriptionruntime.NewCredential(nil, "account-a", subscriptionruntime.Account{}, time.Time{}, false, nil)
	if !result.finish(fresh, true) {
		t.Fatal("expected finish for unchanged local account")
	}
	if result.Reason != "same_account" {
		t.Fatalf("reason = %q, want same_account", result.Reason)
	}
}

func TestKiroRediscoverFinishEmptyIdentity(t *testing.T) {
	var result KiroRediscoverResult
	result.PreviousAccount = "account-a"
	if !result.finish(subscriptionruntime.NewCredential(nil, "", subscriptionruntime.Account{}, time.Time{}, false, nil), true) {
		t.Fatal("expected finish when discovered identity is empty")
	}
	if result.Reason != "same_account" {
		t.Fatalf("reason = %q, want same_account", result.Reason)
	}
}

func TestKiroRediscoverFinishDifferentAccountProceeds(t *testing.T) {
	var result KiroRediscoverResult
	result.PreviousAccount = "account-a"
	fresh := subscriptionruntime.NewCredential(nil, "account-b", subscriptionruntime.Account{}, time.Time{}, false, nil)
	if result.finish(fresh, true) {
		t.Fatal("expected a swap to proceed for a different account")
	}
	if result.Swapped || result.Reason != "" {
		t.Fatalf("unexpected completion state: swapped=%v reason=%q", result.Swapped, result.Reason)
	}
}
