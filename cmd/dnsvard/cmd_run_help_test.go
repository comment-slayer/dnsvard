package main

import "testing"

func TestRunHelpRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "top level help flag", args: []string{"--help"}, want: true},
		{name: "short help flag", args: []string{"-h"}, want: true},
		{name: "help after service", args: []string{"api", "--help"}, want: true},
		{name: "help passed to child command after separator", args: []string{"--", "bun", "dev", "--help"}, want: false},
		{name: "no help", args: []string{"--", "bun", "dev"}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := runHelpRequested(tt.args); got != tt.want {
				t.Fatalf("runHelpRequested(%v) = %t, want %t", tt.args, got, tt.want)
			}
		})
	}
}
