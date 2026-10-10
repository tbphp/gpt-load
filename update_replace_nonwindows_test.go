//go:build !windows

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"testing"

	"gpt-load/internal/releasecheck"
)

func TestSelfUpdaterKeepsExecutableOwnerAndGroup(t *testing.T) {
	executable := writeTestExecutable(t, "old binary")
	original := fileOwner(t, executable)
	group, ok := memberGroupOtherThan(t, original.Gid)
	if !ok {
		t.Skip("current user has no second group to verify group preservation")
	}
	if err := os.Lchown(executable, -1, int(group)); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("new binary")
	server := newReleaseAssetServer(t, map[string][]byte{
		"/download/v2.0.0-rc.2/SHA256SUMS":           []byte(sha256SumsLine(newBinary, "gpt-load-linux-amd64")),
		"/download/v2.0.0-rc.2/gpt-load-linux-amd64": newBinary,
	})
	fetcher := &fakeReleaseFetcher{releases: []releasecheck.Release{testUpdateRelease("v2.0.0-rc.2")}}
	var stdout bytes.Buffer
	updater := newTestSelfUpdater("v2.0.0-rc.1", fetcher, server, executable, &stdout)

	if err := updater.run(t.Context()); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	assertExecutableContent(t, executable, string(newBinary))
	if got := fileOwner(t, executable); got.Uid != original.Uid || got.Gid != group {
		t.Fatalf("executable owner = %d:%d, want %d:%d", got.Uid, got.Gid, original.Uid, group)
	}
}

func TestPreserveExecutableOwnerFailsWhenGroupCannotBeKept(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can assign any group")
	}
	executable := writeTestExecutable(t, "binary")
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(executable, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	original := ownerOverrideInfo{
		FileInfo: info,
		stat:     &syscall.Stat_t{Uid: uint32(os.Geteuid()), Gid: groupOutsideMembership(t)},
	}

	if err := preserveExecutableOwner(file, original); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("preserveExecutableOwner() error = %v, want permission error", err)
	}
}

type ownerOverrideInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (info ownerOverrideInfo) Sys() any {
	return info.stat
}

func fileOwner(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat of %s does not expose owner", path)
	}
	return stat
}

func memberGroupOtherThan(t *testing.T, gid uint32) (uint32, bool) {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if uint32(group) != gid {
			return uint32(group), true
		}
	}
	return 0, false
}

func groupOutsideMembership(t *testing.T) uint32 {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	member := map[int]bool{os.Getegid(): true}
	for _, group := range groups {
		member[group] = true
	}
	for candidate := 1; candidate < 1<<16; candidate++ {
		if !member[candidate] {
			return uint32(candidate)
		}
	}
	t.Skip("no group outside the current user's membership")
	return 0
}
