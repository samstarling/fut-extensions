// Command publish shows the pull request for each live fut workspace's branch.
// Build it into bin/publish with `mise run build`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/samstarling/fut-extensions/internal/fut"
)

const prFields = "number,url,state,isDraft,reviewDecision,statusCheckRollup"

var failedConclusions = []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "ERROR"}

type config struct {
	DefaultBranches []string `json:"default_branches"`
}

// check is one entry of statusCheckRollup: a CheckRun (status, conclusion)
// or a StatusContext (state).
type check struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type pullRequest struct {
	Number            int     `json:"number"`
	URL               string  `json:"url"`
	State             string  `json:"state"`
	IsDraft           bool    `json:"isDraft"`
	ReviewDecision    string  `json:"reviewDecision"`
	StatusCheckRollup []check `json:"statusCheckRollup"`
}

func main() {
	fut.Main(func(client *fut.Client) (fut.Extension, error) {
		if _, err := exec.LookPath("gh"); err != nil {
			return fut.Extension{}, errors.New("gh not found on PATH")
		}
		id := fut.ExtensionID("github")
		cfg := config{DefaultBranches: []string{"main", "master"}}
		if err := client.LoadConfig(id, &cfg); err != nil {
			return fut.Extension{}, err
		}
		return fut.Extension{
			ID:              id,
			Tokens:          []string{"pr", "state", "checks"},
			DefaultBranches: cfg.DefaultBranches,
			Lookup:          lookup,
		}, nil
	})
}

func lookup(ctx context.Context, workspace fut.Workspace, branch string) (*fut.Found, error) {
	dir := workspace.Dir()
	if dir == "" {
		return nil, errors.New("workspace directory no longer exists")
	}
	pr, err := find(ctx, dir, branch)
	if err != nil || pr == nil {
		return nil, err
	}
	return &fut.Found{URL: pr.URL, Values: map[string]string{
		"pr":     "#" + strconv.Itoa(pr.Number),
		"state":  state(*pr),
		"checks": checks(*pr),
	}}, nil
}

func find(ctx context.Context, dir, branch string) (*pullRequest, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch, "--state", "all", "--limit", "1", "--json", prFields)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var prs []pullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

func state(pr pullRequest) string {
	switch {
	case pr.State == "MERGED":
		return "merged"
	case pr.State == "CLOSED":
		return "closed"
	case pr.IsDraft:
		return "draft"
	case pr.ReviewDecision == "APPROVED":
		return "approved"
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return "changes_requested"
	}
	return "open"
}

func checks(pr pullRequest) string {
	if pr.State != "OPEN" || len(pr.StatusCheckRollup) == 0 {
		return ""
	}
	for _, c := range pr.StatusCheckRollup {
		outcome := c.Conclusion
		if outcome == "" {
			outcome = c.State
		}
		if slices.Contains(failedConclusions, outcome) {
			return "failing"
		}
	}
	for _, c := range pr.StatusCheckRollup {
		if (c.Status != "" && c.Status != "COMPLETED") || c.State == "PENDING" || c.State == "EXPECTED" {
			return "pending"
		}
	}
	return "passing"
}
