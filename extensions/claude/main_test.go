package main

import (
	"encoding/json"
	"testing"
)

func TestTokensAndLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  tokens
		line  string
	}{
		{
			"everything",
			`{"model":{"display_name":"Opus 5.5"},"effort":{"level":"high"},"context_window":{"used_percentage":41.6}}`,
			tokens{Model: "Opus 5.5", Effort: "high", Context: "42%"},
			"Opus 5.5 · high · 42% ctx",
		},
		{
			"model without effort support",
			`{"model":{"display_name":"Haiku 4.5"},"context_window":{"used_percentage":3}}`,
			tokens{Model: "Haiku 4.5", Context: "3%"},
			"Haiku 4.5 · 3% ctx",
		},
		{
			"no usage before the first response",
			`{"model":{"display_name":"Opus 5.5"},"effort":{"level":"max"},"context_window":{"used_percentage":null}}`,
			tokens{Model: "Opus 5.5", Effort: "max"},
			"Opus 5.5 · max",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s status
			if err := json.Unmarshal([]byte(tt.input), &s); err != nil {
				t.Fatal(err)
			}
			got := tokensFor(s)
			if got != tt.want {
				t.Errorf("tokensFor() = %+v, want %+v", got, tt.want)
			}
			if line := got.line(); line != tt.line {
				t.Errorf("line() = %q, want %q", line, tt.line)
			}
		})
	}
}
