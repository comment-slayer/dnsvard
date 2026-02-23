package main

import (
	"errors"
	"testing"
)

func TestUserFacingPathTextReplacesHomePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := userFacingPathText("parse config " + home + "/.config/dnsvard/config.yaml")
	want := "parse config ~/.config/dnsvard/config.yaml"
	if got != want {
		t.Fatalf("userFacingPathText = %q, want %q", got, want)
	}
}

func TestUserFacingErrorReplacesHomePrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := errors.New("parse config " + home + "/.config/dnsvard/config.yaml: bad key")
	got := userFacingError(err)
	want := "parse config ~/.config/dnsvard/config.yaml: bad key"
	if got != want {
		t.Fatalf("userFacingError = %q, want %q", got, want)
	}
}
