// Command statusline is a Claude Code status line that also publishes the
// session's model, effort and context usage to the fut pane it runs in.
// Build it into bin/statusline with `mise run build`.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/samstarling/fut-extensions/internal/fut"
)

const extensionID = "claude"

// status is the subset of Claude Code's status line input this command uses.
// See https://code.claude.com/docs/en/statusline#available-data.
type status struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Effort *struct {
		Level string `json:"level"`
	} `json:"effort"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
}

type tokens struct {
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Context string `json:"context"`
}

func tokensFor(s status) tokens {
	t := tokens{Model: s.Model.DisplayName}
	if s.Effort != nil {
		t.Effort = s.Effort.Level
	}
	if used := s.ContextWindow.UsedPercentage; used != nil {
		t.Context = fmt.Sprintf("%.0f%%", *used)
	}
	return t
}

func (t tokens) line() string {
	parts := []string{}
	for _, part := range []string{t.Model, t.Effort} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if t.Context != "" {
		parts = append(parts, t.Context+" ctx")
	}
	return strings.Join(parts, " · ")
}

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	var s status
	if err := json.Unmarshal(input, &s); err != nil {
		os.Exit(1)
	}
	t := tokensFor(s)
	fmt.Println(t.line())

	if pane := os.Getenv("FUT_PANE_ID"); pane != "" {
		if err := publish(pane, t); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}

// publish skips tokens that haven't changed since the last update, since
// Claude Code reruns the status line after every message.
func publish(pane string, t tokens) error {
	cache := filepath.Join(fut.CacheDir(extensionID), pane+".json")
	var last tokens
	if previous, err := os.ReadFile(cache); err == nil {
		_ = json.Unmarshal(previous, &last)
	}
	if t == last {
		return nil
	}
	client, err := fut.NewClient()
	if err != nil {
		return err
	}
	for _, token := range []struct{ name, value, last string }{
		{"model", t.Model, last.Model},
		{"effort", t.Effort, last.Effort},
		{"context", t.Context, last.Context},
	} {
		if token.value == token.last {
			continue
		}
		if err := client.PublishPane(extensionID, pane, token.name, token.value); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(cache, encoded, 0o644)
}
