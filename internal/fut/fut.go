// Package fut implements the parts of fut's extension API v1 that the
// extensions in this repository share: listing workspaces, reading extension
// config and hook payloads, and publishing presentation tokens.
package fut

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrNoDaemon means fut isn't running, so there is nothing to publish to.
var ErrNoDaemon = errors.New("fut daemon is not reachable")

// Client runs the fut CLI against the daemon that launched the extension.
type Client struct {
	bin    string
	socket string
	env    []string
}

// NewClient finds fut through FUT_BIN (set by fut for commands and hooks) or PATH.
func NewClient() (*Client, error) {
	bin := os.Getenv("FUT_BIN")
	if bin == "" {
		found, err := exec.LookPath("fut")
		if err != nil {
			return nil, errors.New("fut not found on PATH; set FUT_BIN")
		}
		bin = found
	}
	env := os.Environ()
	// launchd jobs have no TMPDIR, and fut derives its socket path from it.
	if os.Getenv("TMPDIR") == "" {
		if out, err := exec.Command("getconf", "DARWIN_USER_TEMP_DIR").Output(); err == nil {
			env = append(env, "TMPDIR="+strings.TrimSpace(string(out)))
		}
	}
	return &Client{bin: bin, socket: os.Getenv("FUT_SOCKET"), env: env}, nil
}

func (c *Client) run(args ...string) ([]byte, error) {
	if c.socket != "" {
		args = append([]string{"--socket", c.socket}, args...)
	}
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// With --json, fut reports failures as a JSON error envelope.
		if message, ok := errorMessage(stderr.Bytes(), out); ok {
			if strings.HasPrefix(message, "connect to ") {
				return nil, ErrNoDaemon
			}
			return nil, fmt.Errorf("fut %s: %s", strings.Join(args, " "), message)
		}
		return nil, fmt.Errorf("fut %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// errorMessage returns the message of the first output that is a fut --json
// error envelope.
func errorMessage(outputs ...[]byte) (string, bool) {
	for _, output := range outputs {
		var envelope struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(output, &envelope) == nil && envelope.Error != nil {
			return strings.TrimSpace(envelope.Error.Message), true
		}
	}
	return "", false
}

// result runs a fut command with --json and decodes its result envelope.
func (c *Client) result(dst any, args ...string) error {
	out, err := c.run(append(args, "--json")...)
	if err != nil {
		return err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return fmt.Errorf("fut %s: %w", args[0], err)
	}
	if err := json.Unmarshal(envelope.Result, dst); err != nil {
		return fmt.Errorf("fut %s: %w", args[0], err)
	}
	return nil
}

// Workspace is one workspace from `fut list --json`.
type Workspace struct {
	ID      string                     `json:"id"`
	Root    string                     `json:"root"`
	Closing bool                       `json:"closing"`
	Tokens  map[string]json.RawMessage `json:"tokens"`
	Tabs    []struct {
		Panes []struct {
			Worktree string `json:"worktree"`
		} `json:"panes"`
	} `json:"tabs"`
}

// Branch is the workspace's git branch, or "" outside a repository.
func (w Workspace) Branch() string {
	var branch string
	_ = json.Unmarshal(w.Tokens["workspace.git_branch"], &branch)
	return branch
}

// Dir is the first pane worktree that still exists, falling back to the
// workspace root, or "" if neither does.
func (w Workspace) Dir() string {
	var candidates []string
	for _, tab := range w.Tabs {
		for _, pane := range tab.Panes {
			if pane.Worktree != "" {
				candidates = append(candidates, pane.Worktree)
			}
		}
	}
	for _, dir := range append(candidates, w.Root) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// Workspaces lists live workspaces. It returns ErrNoDaemon when fut isn't running.
func (c *Client) Workspaces() ([]Workspace, error) {
	var listing struct {
		Sessions []struct {
			Workspaces []Workspace `json:"workspaces"`
		} `json:"sessions"`
	}
	if err := c.result(&listing, "list"); err != nil {
		return nil, err
	}
	var live []Workspace
	for _, session := range listing.Sessions {
		for _, workspace := range session.Workspaces {
			if !workspace.Closing {
				live = append(live, workspace)
			}
		}
	}
	return live, nil
}

// LoadConfig decodes `[extension.<id>]` onto dst, which should already hold
// the defaults. Commands and hooks receive it in FUT_EXTENSION_CONFIG; launchd
// runs ask the daemon for the global config instead.
func (c *Client) LoadConfig(extensionID string, dst any) error {
	raw := []byte(os.Getenv("FUT_EXTENSION_CONFIG"))
	if len(raw) == 0 {
		var shown struct {
			Config struct {
				Defaults json.RawMessage `json:"defaults"`
			} `json:"config"`
		}
		if err := c.result(&shown, "extension", "show", extensionID); err != nil {
			return err
		}
		raw = shown.Config.Defaults
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid [extension.%s] config: %w", extensionID, err)
	}
	return nil
}

// Publish sets one workspace-scoped token. A non-empty action names a command
// of the same extension to run when the token is clicked.
func (c *Client) Publish(extensionID, workspaceID, token, value, action string) error {
	args := []string{"token", "publish", extensionID, token, value, "--workspace-id", workspaceID}
	if action != "" && value != "" {
		args = append(args, "--action-command", action)
	}
	_, err := c.run(args...)
	return err
}

// Matches reports whether id names this workspace, in either the UUID form
// from `fut list` and hook payloads or the compact form commands receive.
func (w Workspace) Matches(id string) bool {
	if id == w.ID {
		return true
	}
	compact, err := CompactID(w.ID)
	return err == nil && compact == id
}

// TargetWorkspace returns the one workspace this run should publish for: the
// workspace a lifecycle hook fired for, or the one a command such as refresh
// ran in, whose workspace-level config only applies to it. It returns "" for
// launchd and manual runs, which publish for every workspace.
func TargetWorkspace(stdin io.Reader) (string, error) {
	event := os.Getenv("FUT_EVENT")
	if event == "" {
		if os.Getenv("FUT_EXTENSION_COMMAND") != "" {
			return os.Getenv("FUT_WORKSPACE_ID"), nil
		}
		return "", nil
	}
	line, err := bufio.NewReader(stdin).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	var payload struct {
		Version   int    `json:"version"`
		Event     string `json:"event"`
		Workspace *struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return "", fmt.Errorf("parse %s hook payload: %w", event, err)
	}
	if payload.Version != 1 || payload.Event != event || payload.Workspace == nil {
		return "", fmt.Errorf("unsupported %s hook payload", event)
	}
	return payload.Workspace.ID, nil
}

// NetworkTimeout is short inside hooks, which fut kills after five seconds.
func NetworkTimeout() time.Duration {
	if os.Getenv("FUT_EVENT") != "" {
		return 2 * time.Second
	}
	return 20 * time.Second
}

// CompactID converts a workspace UUID from `fut list --json` to the compact
// form fut passes to commands in FUT_WORKSPACE_ID.
func CompactID(workspaceID string) (string, error) {
	raw, err := hex.DecodeString(strings.ReplaceAll(workspaceID, "-", ""))
	if err != nil || len(raw) != 16 {
		return "", fmt.Errorf("invalid workspace ID %q", workspaceID)
	}
	return "f" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// CacheDir holds one file per workspace with the URL its `open` command opens.
func CacheDir(extensionID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Caches/fut-extensions", extensionID)
}

// Found is what an extension looked up for one workspace's branch.
type Found struct {
	URL    string            // opened by the extension's `open` command
	Values map[string]string // token name to published value
}

// Extension describes one branch-status extension for Run.
type Extension struct {
	ID              string
	Tokens          []string // every published token; the first opens the URL when clicked
	DefaultBranches []string // branches that never have anything to show
	// Lookup returns nil when the branch has nothing to show, and an error to
	// keep the last published values, such as during a network blip.
	Lookup func(ctx context.Context, workspace Workspace, branch string) (*Found, error)
}

// Publisher publishes presentation tokens; *Client is the real one.
type Publisher interface {
	Publish(extensionID, workspaceID, token, value, action string) error
}

// Run publishes ext's tokens for each live workspace, or only for the
// workspace target names when that is non-empty. Problems with one workspace
// are logged and skipped so they can't stop the others from updating.
func Run(client Publisher, ext Extension, workspaces []Workspace, target string) error {
	started := time.Now()
	cache := CacheDir(ext.ID)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	live := map[string]bool{}
	matched := false
	for _, workspace := range workspaces {
		if target != "" && !workspace.Matches(target) {
			continue
		}
		matched = true
		compact, err := CompactID(workspace.ID)
		if err != nil {
			log.Print(err)
			continue
		}
		live[compact] = true
		var found *Found
		branch := workspace.Branch()
		if branch != "" && !slices.Contains(ext.DefaultBranches, branch) {
			ctx, cancel := context.WithTimeout(context.Background(), NetworkTimeout())
			found, err = ext.Lookup(ctx, workspace, branch)
			cancel()
			if err != nil {
				log.Printf("%s: %v", branch, err)
				continue
			}
		}
		urlFile := filepath.Join(cache, compact)
		if found != nil {
			err = os.WriteFile(urlFile, []byte(found.URL), 0o644)
		} else if err = os.Remove(urlFile); errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		if err != nil {
			log.Print(err) // Only the open command needs the cache, so still publish.
		}
		for i, token := range ext.Tokens {
			value, action := "", ""
			if found != nil {
				value = found.Values[token]
				if i == 0 {
					action = "open" // fut rejects actions on empty values.
				}
			}
			if err := client.Publish(ext.ID, workspace.ID, token, value, action); err != nil {
				log.Print(err)
			}
		}
	}
	if target != "" {
		if !matched {
			log.Printf("workspace %s is not in `fut list`", target)
		}
		return nil // A targeted run only sees one workspace, so it can't tell which cache files are stale.
	}
	return removeStale(cache, live, started)
}

// removeStale deletes cache files for workspaces that have closed. Files
// written since the run started are kept: they belong to workspaces created
// after it listed them, whose hooks have already published.
func removeStale(cache string, live map[string]bool, started time.Time) error {
	entries, err := os.ReadDir(cache)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if live[entry.Name()] {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(started) {
			continue
		}
		if err := os.Remove(filepath.Join(cache, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Print(err)
		}
	}
	return nil
}

// Main builds the extension with setup, which can load config through the
// client, then runs it. It exits quietly when fut isn't running.
func Main(setup func(*Client) (Extension, error)) {
	if os.Getenv("FUT_EXTENSION_COMMAND") != "" || os.Getenv("FUT_EVENT") != "" {
		log.SetFlags(0)
	}
	if err := start(setup); err != nil && !errors.Is(err, ErrNoDaemon) {
		log.Fatal(err)
	}
}

func start(setup func(*Client) (Extension, error)) error {
	client, err := NewClient()
	if err != nil {
		return err
	}
	workspaces, err := client.Workspaces()
	if err != nil {
		return err
	}
	target, err := TargetWorkspace(os.Stdin)
	if err != nil {
		return err
	}
	ext, err := setup(client)
	if err != nil {
		return err
	}
	return Run(client, ext, workspaces, target)
}

// ExtensionID is the manifest ID fut passes in FUT_EXTENSION_ID, or fallback
// when run outside fut, such as by launchd.
func ExtensionID(fallback string) string {
	if id := os.Getenv("FUT_EXTENSION_ID"); id != "" {
		return id
	}
	return fallback
}
