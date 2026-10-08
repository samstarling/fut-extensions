// Command publish shows the Linear issue for each live fut workspace's branch.
// Build it into bin/publish with `mise run build`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"

	"github.com/samstarling/fut-extensions/internal/fut"
)

const issueFields = "identifier url state { name type }"

var identifierPattern = regexp.MustCompile(`(?i)\b([a-z]+-\d+)\b`)

type config struct {
	DefaultBranches []string `json:"default_branches"`
	KeychainService string   `json:"keychain_service"`
}

type issue struct {
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
	State      struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
}

func main() {
	fut.Main(func(client *fut.Client) (fut.Extension, error) {
		id := fut.ExtensionID("linear")
		cfg := config{DefaultBranches: []string{"main", "master"}, KeychainService: "fut-linear-api-key"}
		if err := client.LoadConfig(id, &cfg); err != nil {
			return fut.Extension{}, err
		}
		key, err := apiKey(cfg.KeychainService)
		if err != nil {
			return fut.Extension{}, err
		}
		return fut.Extension{
			ID:              id,
			Tokens:          []string{"issue", "status", "status_icon"},
			DefaultBranches: cfg.DefaultBranches,
			Lookup: func(ctx context.Context, _ fut.Workspace, branch string) (*fut.Found, error) {
				found, err := find(ctx, key, branch)
				if err != nil || found == nil {
					return nil, err
				}
				return &fut.Found{URL: found.URL, Values: map[string]string{
					"issue":       found.Identifier,
					"status":      found.State.Name,
					"status_icon": found.State.Type,
				}}, nil
			},
		}, nil
	})
}

func apiKey(service string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no Linear API key in the login keychain; add one with:\n"+
			"  security add-generic-password -s %s -a \"$USER\" -w", service)
	}
	return strings.TrimSpace(string(out)), nil
}

// find looks the branch up as Linear's linked branch name, then falls back to
// an issue identifier such as ENG-123 within it.
func find(ctx context.Context, key, branch string) (*issue, error) {
	var byBranch struct {
		Issue *issue `json:"issueVcsBranchSearch"`
	}
	query := fmt.Sprintf("query($branch: String!) { issueVcsBranchSearch(branchName: $branch) { %s } }", issueFields)
	if err := graphql(ctx, key, query, map[string]any{"branch": branch}, &byBranch); err != nil {
		return nil, err
	}
	if byBranch.Issue != nil {
		return byBranch.Issue, nil
	}
	identifier := issueIdentifier(branch)
	if identifier == "" {
		return nil, nil
	}
	var byID struct {
		Issue *issue `json:"issue"`
	}
	query = fmt.Sprintf("query($id: String!) { issue(id: $id) { %s } }", issueFields)
	if err := graphql(ctx, key, query, map[string]any{"id": identifier}, &byID); err != nil {
		return nil, err
	}
	return byID.Issue, nil
}

func issueIdentifier(branch string) string {
	match := identifierPattern.FindStringSubmatch(branch)
	if match == nil {
		return ""
	}
	return strings.ToUpper(match[1])
}

// graphql decodes the response's data into dst. An HTTP error fails, but a
// GraphQL error, such as an unknown issue ID, leaves dst's fields nil.
func graphql(ctx context.Context, key, query string, variables map[string]any, dst any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", key)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: %s", response.Status)
	}
	var decoded struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	if len(decoded.Data) == 0 || string(decoded.Data) == "null" {
		return nil
	}
	return json.Unmarshal(decoded.Data, dst)
}
