//go:build linux || darwin

package viewer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func managedTestDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "managed")
}
func openTestStore(t *testing.T, path string) *PrivateStateStore {
	t.Helper()
	s, err := OpenPrivateState(context.Background(), path, "test-installation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestManagedStoreDurabilityRecovery(t *testing.T) {
	ctx := context.Background()
	path := managedTestDir(t)
	s := openTestStore(t, path)
	state := ManagedState{Revision: 1, CommitID: "commit-one", Payload: []byte(`{ "camera": "one" }`)}
	s.files.syncDirHook = func() error { return errors.New("injected directory sync failure") }
	outcome, err := s.Commit(ctx, 0, state)
	if outcome != CommitUnknown || err == nil {
		t.Fatal("acknowledged failed directory sync", outcome, err)
	}
	if _, err = s.Load(ctx); err == nil {
		t.Fatal("exposed readable but uncommitted state")
	}
	if _, _, err = s.Recover(ctx); err == nil {
		t.Fatal("recovery skipped directory durability")
	}
	if outcome, _ = s.Commit(ctx, 0, state); outcome != CommitUnknown {
		t.Fatal("writes not frozen")
	}
	s.files.syncDirHook = nil
	s.files.syncFileHook = func() error { return errors.New("injected file sync failure") }
	if _, _, err = s.Recover(ctx); err == nil {
		t.Fatal("recovery skipped named-file durability")
	}
	s.files.syncFileHook = nil
	recovered, outcome, err := s.Recover(ctx)
	if err != nil || outcome != CommitDurable || recovered.Revision != 1 {
		t.Fatal(outcome, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openTestStore(t, path)
	loaded, err := restarted.Load(ctx)
	if err != nil || !sameManagedState(recovered, loaded) {
		t.Fatal("restart lost acknowledged state", err)
	}
}
func TestManagedStoreRenameFailureAndRestart(t *testing.T) {
	ctx := context.Background()
	path := managedTestDir(t)
	s := openTestStore(t, path)
	one := ManagedState{1, "commit-one", []byte(`{"camera":"one"}`)}
	if outcome, err := s.Commit(ctx, 0, one); err != nil || outcome != CommitDurable {
		t.Fatal(outcome, err)
	}
	s.files.renameHook = func() error { return errors.New("rename failed") }
	if outcome, err := s.Commit(ctx, 1, ManagedState{2, "commit-two", []byte(`{"camera":"two"}`)}); outcome != CommitUnknown || err == nil {
		t.Fatal(outcome, err)
	}
	old, outcome, err := s.Recover(ctx)
	if err != nil || outcome != CommitNotCommitted || !sameManagedState(one, old) {
		t.Fatal(outcome, err)
	}
	if _, err = os.Stat(filepath.Join(path, managedCandidateName)); !os.IsNotExist(err) {
		t.Fatal("candidate not cleaned")
	}
	s.files.renameHook = nil
	s.files.syncDirHook = func() error { return errors.New("sync failed") }
	if outcome, _ = s.Commit(ctx, 1, ManagedState{2, "commit-three", []byte(`{"camera":"two"}`)}); outcome != CommitUnknown {
		t.Fatal(outcome)
	}
	_ = s.Close()
	restarted := openTestStore(t, path)
	loaded, err := restarted.Load(ctx)
	if err != nil || loaded.Revision != 2 {
		t.Fatal("startup did not recover selected envelope", err)
	}
}
func TestManagedStoreRejectsUnsafeStorage(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []os.FileMode{0755, 0770, os.ModeSticky | 0700} {
		t.Run(mode.String(), func(t *testing.T) {
			path := managedTestDir(t)
			if err := os.Mkdir(path, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if s, err := OpenPrivateState(ctx, path, "test-installation"); err == nil {
				_ = s.Close()
				t.Fatal("unsafe mode accepted")
			}
		})
	}
	path := managedTestDir(t)
	s := openTestStore(t, path)
	if other, err := OpenPrivateState(ctx, path, "other"); err == nil {
		_ = other.Close()
		t.Fatal("concurrent writer accepted")
	}
	_ = s.Close()
	if other, err := OpenPrivateState(ctx, path, "other"); err == nil {
		_ = other.Close()
		t.Fatal("wrong installation accepted")
	}
	for _, body := range []string{`{`, `{"version":999}`, strings.Repeat("x", maxManagedStateBytes+1)} {
		if err := os.WriteFile(filepath.Join(path, managedStateName), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if other, err := OpenPrivateState(ctx, path, "test-installation"); err == nil {
			_ = other.Close()
			t.Fatal("corrupt state accepted")
		}
	}
	if err := os.Remove(filepath.Join(path, managedStateName)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(path), "target")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(path, managedStateName)); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenPrivateState(ctx, path, "test-installation"); err == nil {
		_ = other.Close()
		t.Fatal("symlink accepted")
	}
}
func TestManagedStoreCopiesAndPermissions(t *testing.T) {
	path := managedTestDir(t)
	s := openTestStore(t, path)
	ctx := context.Background()
	input := ManagedState{1, "commit-one", []byte(`{"secret":"private"}`)}
	if _, err := s.Commit(ctx, 0, input); err != nil {
		t.Fatal(err)
	}
	clear(input.Payload)
	first, _ := s.Load(ctx)
	clear(first.Payload)
	second, _ := s.Load(ctx)
	if !strings.Contains(string(second.Payload), "private") {
		t.Fatal("borrowed state escaped")
	}
	for _, name := range []string{managedStateName, "managed.lock"} {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("not private", err)
		}
	}
}

func TestManagedStateRejectsDuplicateKeys(t *testing.T) {
	for _, body := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{"a":1} {"b":2}`} {
		if uniqueJSON([]byte(body)) {
			t.Fatal("ambiguous state accepted")
		}
	}
	if !uniqueJSON([]byte(`{"a":[{"b":1},{"b":2}]}`)) {
		t.Fatal("valid nested state rejected")
	}
}

func TestManagedStoreAbandonedCandidateRestart(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, body := range []string{"", `{"version":`, `{"version":999}`} {
			t.Run(fmt.Sprintf("committed=%t/bytes=%d", committed, len(body)), func(t *testing.T) {
				ctx := context.Background()
				path := managedTestDir(t)
				s := openTestStore(t, path)
				want := ManagedState{}
				if committed {
					want = ManagedState{1, "commit-one", []byte(`{"credential":"private"}`)}
					if _, err := s.Commit(ctx, 0, want); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, managedCandidateName), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					restarted := openTestStore(t, path)
					got, err := restarted.Load(ctx)
					if err != nil || !sameManagedState(got, want) {
						t.Fatal("committed generation changed", err)
					}
					if _, err := os.Lstat(filepath.Join(path, managedCandidateName)); !os.IsNotExist(err) {
						t.Fatal("abandoned candidate retained", err)
					}
					if err := restarted.Close(); err != nil {
						t.Fatal(err)
					}
				}
				restarted := openTestStore(t, path)
				if _, err := restarted.Commit(ctx, want.Revision, ManagedState{want.Revision + 1, "commit-next", []byte(`{"credential":"next"}`)}); err != nil {
					t.Fatal("candidate blocked next commit", err)
				}
			})
		}
	}
}

func TestManagedStoreCandidateSafety(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "mode", "directory"} {
		t.Run(kind, func(t *testing.T) {
			path := managedTestDir(t)
			s := openTestStore(t, path)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(filepath.Dir(path), "target")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(path, managedCandidateName)
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, candidate)
			case "hardlink":
				err = os.Link(target, candidate)
			case "mode":
				err = os.WriteFile(candidate, nil, 0644)
			case "directory":
				err = os.Mkdir(candidate, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if restarted, err := OpenPrivateState(context.Background(), path, "test-installation"); err == nil {
				_ = restarted.Close()
				t.Fatal("unsafe candidate accepted")
			}
			if _, err := os.Lstat(candidate); err != nil {
				t.Fatal("unsafe candidate removed", err)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "untouched" {
				t.Fatal("target changed", err)
			}
		})
	}
}

func TestManagedStoreRejectsInvalidPayloadBeforePreparing(t *testing.T) {
	path := managedTestDir(t)
	s := openTestStore(t, path)
	ctx := context.Background()
	if outcome, err := s.Commit(ctx, 0, ManagedState{1, "commit-bad", []byte(`{"secret":1,"secret":2}`)}); err == nil || outcome != CommitNotCommitted {
		t.Fatal("ambiguous payload accepted")
	}
	if _, err := os.Lstat(filepath.Join(path, managedCandidateName)); !os.IsNotExist(err) {
		t.Fatal("invalid commit prepared a candidate", err)
	}
	if _, err := s.Commit(ctx, 0, ManagedState{1, "commit-good", []byte(`{"secret":1}`)}); err != nil {
		t.Fatal("invalid payload blocked writes", err)
	}
}
