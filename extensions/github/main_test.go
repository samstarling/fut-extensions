package main

import "testing"

func pr(state string, draft bool, review string, rollup ...check) pullRequest {
	return pullRequest{State: state, IsDraft: draft, ReviewDecision: review, StatusCheckRollup: rollup}
}

func TestState(t *testing.T) {
	tests := []struct {
		name string
		pr   pullRequest
		want string
	}{
		{"merged beats draft and review", pr("MERGED", true, "APPROVED"), "merged"},
		{"closed beats draft", pr("CLOSED", true, ""), "closed"},
		{"draft beats review", pr("OPEN", true, "APPROVED"), "draft"},
		{"approved", pr("OPEN", false, "APPROVED"), "approved"},
		{"changes requested", pr("OPEN", false, "CHANGES_REQUESTED"), "changes_requested"},
		{"review required", pr("OPEN", false, "REVIEW_REQUIRED"), "open"},
		{"no review", pr("OPEN", false, ""), "open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := state(tt.pr); got != tt.want {
				t.Errorf("state() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChecks(t *testing.T) {
	tests := []struct {
		name string
		pr   pullRequest
		want string
	}{
		{"no checks", pr("OPEN", false, ""), ""},
		{"not open", pr("MERGED", false, "", check{Status: "COMPLETED", Conclusion: "FAILURE"}), ""},
		{
			"failing beats pending",
			pr("OPEN", false, "", check{Status: "IN_PROGRESS"}, check{Status: "COMPLETED", Conclusion: "FAILURE"}),
			"failing",
		},
		{"failing status context", pr("OPEN", false, "", check{State: "ERROR"}), "failing"},
		{"queued check run", pr("OPEN", false, "", check{Status: "QUEUED"}), "pending"},
		{"pending status context", pr("OPEN", false, "", check{State: "PENDING"}), "pending"},
		{
			"passing includes neutral and skipped",
			pr("OPEN", false, "",
				check{Status: "COMPLETED", Conclusion: "SUCCESS"},
				check{Status: "COMPLETED", Conclusion: "SKIPPED"},
				check{Status: "COMPLETED", Conclusion: "NEUTRAL"},
				check{State: "SUCCESS"},
			),
			"passing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checks(tt.pr); got != tt.want {
				t.Errorf("checks() = %q, want %q", got, tt.want)
			}
		})
	}
}
