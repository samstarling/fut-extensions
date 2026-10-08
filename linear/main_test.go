package main

import "testing"

func TestIssueIdentifier(t *testing.T) {
	tests := []struct {
		name, branch, want string
	}{
		{"identifier after a prefix", "sam/eng-123-fix-login", "ENG-123"},
		{"identifier alone", "ENG-9", "ENG-9"},
		{"no identifier", "fix-login", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := issueIdentifier(tt.branch); got != tt.want {
				t.Errorf("issueIdentifier() = %q, want %q", got, tt.want)
			}
		})
	}
}
