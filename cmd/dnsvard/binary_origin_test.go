package main

import "testing"

func TestIsGoRunExecutable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want bool
	}{
		{path: "/var/folders/ab/cd/T/go-build12345/b001/exe/dnsvard", want: true},
		{path: "/tmp/go-build9876/b001/exe/dnsvard", want: true},
		{path: "/Users/some-user/.local/bin/dnsvard", want: false},
	}
	for _, tc := range cases {
		got := isGoRunExecutable(tc.path)
		if got != tc.want {
			t.Fatalf("isGoRunExecutable(%q) = %t, want %t", tc.path, got, tc.want)
		}
	}
}
