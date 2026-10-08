package fut

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompactID(t *testing.T) {
	got, err := CompactID("913b2694-478b-40c3-a313-22c95339147c")
	if err != nil || got != "fkTsmlEeLQMOjEyLJUzkUfA" {
		t.Errorf("CompactID() = %q, %v", got, err)
	}
	if _, err := CompactID("not-a-uuid"); err == nil {
		t.Error("CompactID() accepted an invalid ID")
	}
}

func TestBranch(t *testing.T) {
	var workspace Workspace
	listing := `{"tokens": {"workspace.git_branch": "sam/eng-1", "workspace.extension.github.pr": {"text": "#1"}}}`
	if err := json.Unmarshal([]byte(listing), &workspace); err != nil {
		t.Fatal(err)
	}
	if got := workspace.Branch(); got != "sam/eng-1" {
		t.Errorf("Branch() = %q", got)
	}
	if got := (Workspace{}).Branch(); got != "" {
		t.Errorf("Branch() without a token = %q", got)
	}
}

func TestTargetWorkspace(t *testing.T) {
	t.Setenv("FUT_EVENT", "")
	t.Setenv("FUT_EXTENSION_COMMAND", "")
	t.Setenv("FUT_WORKSPACE_ID", "fCompact")
	if id, err := TargetWorkspace(strings.NewReader("")); id != "" || err != nil {
		t.Errorf("launchd or manual run: %q, %v", id, err)
	}

	t.Setenv("FUT_EXTENSION_COMMAND", "refresh")
	if id, err := TargetWorkspace(strings.NewReader("")); id != "fCompact" || err != nil {
		t.Errorf("command: %q, %v", id, err)
	}

	t.Setenv("FUT_EVENT", "workspace.created")
	payload := `{"version": 1, "event": "workspace.created", "workspace": {"id": "W", "root": "/"}, "extra": true}` + "\n"
	if id, err := TargetWorkspace(strings.NewReader(payload)); id != "W" || err != nil {
		t.Errorf("valid payload: %q, %v", id, err)
	}
	for _, bad := range []string{
		`{"version": 2, "event": "workspace.created", "workspace": {"id": "W"}}`,
		`{"version": 1, "event": "workspace.closed", "workspace": {"id": "W"}}`,
		`{"version": 1, "event": "workspace.created"}`,
		`not json`,
	} {
		if _, err := TargetWorkspace(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestMatches(t *testing.T) {
	workspace := Workspace{ID: "913b2694-478b-40c3-a313-22c95339147c"}
	for id, want := range map[string]bool{
		"913b2694-478b-40c3-a313-22c95339147c": true,
		"fkTsmlEeLQMOjEyLJUzkUfA":              true,
		"fSomethingElse":                       false,
	} {
		if got := workspace.Matches(id); got != want {
			t.Errorf("Matches(%q) = %v, want %v", id, got, want)
		}
	}
}

const (
	idA = "00000000-0000-0000-0000-00000000000a"
	idB = "00000000-0000-0000-0000-00000000000b"
)

type published struct{ workspaceID, token, value, action string }

type fakePublisher struct{ calls []published }

func (f *fakePublisher) Publish(_, workspaceID, token, value, action string) error {
	f.calls = append(f.calls, published{workspaceID, token, value, action})
	return nil
}

func (f *fakePublisher) workspaces() []string {
	var ids []string
	for _, call := range f.calls {
		if !slices.Contains(ids, call.workspaceID) {
			ids = append(ids, call.workspaceID)
		}
	}
	return ids
}

func workspaceOn(id, branch string) Workspace {
	return Workspace{ID: id, Tokens: map[string]json.RawMessage{"workspace.git_branch": json.RawMessage(`"` + branch + `"`)}}
}

// testExtension finds a PR for every branch except "none", and fails for "down".
func testExtension() Extension {
	return Extension{
		ID:              "test",
		Tokens:          []string{"pr", "state"},
		DefaultBranches: []string{"main"},
		Lookup: func(_ context.Context, _ Workspace, branch string) (*Found, error) {
			switch branch {
			case "none":
				return nil, nil
			case "down":
				return nil, errors.New("network down")
			}
			return &Found{URL: "https://example.com/" + branch, Values: map[string]string{"pr": "#1", "state": "open"}}, nil
		},
	}
}

// setUpCache points CacheDir at a temporary home and returns the test extension's cache.
func setUpCache(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FUT_EVENT", "")
	cache := CacheDir("test")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	return cache
}

func writeCache(t *testing.T, cache, workspaceID string, modified time.Time) string {
	t.Helper()
	compact, err := CompactID(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache, compact)
	if err := os.WriteFile(path, []byte("https://example.com/old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRunPublishesAndCachesURL(t *testing.T) {
	cache := setUpCache(t)
	publisher := &fakePublisher{}
	if err := Run(publisher, testExtension(), []Workspace{workspaceOn(idA, "feature")}, ""); err != nil {
		t.Fatal(err)
	}
	want := []published{{idA, "pr", "#1", "open"}, {idA, "state", "open", ""}}
	if !slices.Equal(publisher.calls, want) {
		t.Errorf("published %v, want %v", publisher.calls, want)
	}
	compact, _ := CompactID(idA)
	if url, _ := os.ReadFile(filepath.Join(cache, compact)); string(url) != "https://example.com/feature" {
		t.Errorf("cached URL = %q", url)
	}
}

func TestRunClearsDefaultBranchesAndMissingPRs(t *testing.T) {
	cache := setUpCache(t)
	stale := writeCache(t, cache, idA, time.Now().Add(-time.Hour))
	publisher := &fakePublisher{}
	workspaces := []Workspace{workspaceOn(idA, "main"), workspaceOn(idB, "none")}
	if err := Run(publisher, testExtension(), workspaces, ""); err != nil {
		t.Fatal(err)
	}
	for _, call := range publisher.calls {
		if call.value != "" || call.action != "" {
			t.Errorf("published %+v, want an empty value without an action", call)
		}
	}
	if len(publisher.calls) != 4 {
		t.Errorf("published %d values, want 4", len(publisher.calls))
	}
	if exists(stale) {
		t.Error("kept the cached URL for a branch with nothing to show")
	}
}

func TestRunKeepsLastValuesWhenLookupFails(t *testing.T) {
	cache := setUpCache(t)
	cached := writeCache(t, cache, idA, time.Now().Add(-time.Hour))
	publisher := &fakePublisher{}
	workspaces := []Workspace{workspaceOn(idA, "down"), workspaceOn(idB, "feature")}
	if err := Run(publisher, testExtension(), workspaces, ""); err != nil {
		t.Fatal(err)
	}
	if got := publisher.workspaces(); !slices.Equal(got, []string{idB}) {
		t.Errorf("published for %v, want only the workspace whose lookup worked", got)
	}
	if !exists(cached) {
		t.Error("deleted the cached URL of a workspace whose lookup failed")
	}
}

func TestRunSkipsInvalidWorkspaceIDs(t *testing.T) {
	setUpCache(t)
	publisher := &fakePublisher{}
	workspaces := []Workspace{workspaceOn("not-a-uuid", "feature"), workspaceOn(idB, "feature")}
	if err := Run(publisher, testExtension(), workspaces, ""); err != nil {
		t.Fatal(err)
	}
	if got := publisher.workspaces(); !slices.Equal(got, []string{idB}) {
		t.Errorf("published for %v, want the valid workspace after the invalid one", got)
	}
}

func TestRunRemovesOnlyOldCacheFilesOfClosedWorkspaces(t *testing.T) {
	cache := setUpCache(t)
	closed := writeCache(t, cache, "00000000-0000-0000-0000-0000000000c1", time.Now().Add(-time.Hour))
	// Written by the hook of a workspace created after this run listed workspaces.
	created := writeCache(t, cache, "00000000-0000-0000-0000-0000000000c2", time.Now().Add(time.Minute))
	if err := Run(&fakePublisher{}, testExtension(), []Workspace{workspaceOn(idA, "feature")}, ""); err != nil {
		t.Fatal(err)
	}
	if exists(closed) {
		t.Error("kept the cache file of a closed workspace")
	}
	if !exists(created) {
		t.Error("removed the cache file of a workspace created during the run")
	}
}

func TestRunTargetsOneWorkspace(t *testing.T) {
	compactB, _ := CompactID(idB)
	for name, target := range map[string]string{"hook UUID": idB, "command compact ID": compactB} {
		t.Run(name, func(t *testing.T) {
			cache := setUpCache(t)
			other := writeCache(t, cache, "00000000-0000-0000-0000-0000000000c1", time.Now().Add(-time.Hour))
			publisher := &fakePublisher{}
			workspaces := []Workspace{workspaceOn(idA, "feature"), workspaceOn(idB, "feature")}
			if err := Run(publisher, testExtension(), workspaces, target); err != nil {
				t.Fatal(err)
			}
			if got := publisher.workspaces(); !slices.Equal(got, []string{idB}) {
				t.Errorf("published for %v, want only %s", got, idB)
			}
			if !exists(other) {
				t.Error("a targeted run removed another workspace's cache file")
			}
		})
	}
}

func TestClientErrors(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fut")
	client := &Client{bin: fake}
	for name, script := range map[string]string{
		"stderr": `echo '{"error":{"code":"command_failed","message":"%s"},"version":1}' >&2; exit 1`,
		"stdout": `echo '{"error":{"code":"command_failed","message":"%s"},"version":1}'; exit 1`,
	} {
		t.Run(name, func(t *testing.T) {
			write := func(message string) {
				body := "#!/bin/sh\n" + strings.ReplaceAll(script, "%s", message) + "\n"
				if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write("connect to /tmp/fut.sock: No such file or directory (os error 2)")
			if _, err := client.Workspaces(); !errors.Is(err, ErrNoDaemon) {
				t.Errorf("unreachable daemon: %v, want ErrNoDaemon", err)
			}
			write("unexpected schema")
			_, err := client.Workspaces()
			if err == nil || errors.Is(err, ErrNoDaemon) || !strings.Contains(err.Error(), "unexpected schema") {
				t.Errorf("other failure: %v, want the fut error message", err)
			}
		})
	}
}
