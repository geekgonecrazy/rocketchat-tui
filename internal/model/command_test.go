package model_test

import (
	"testing"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

func TestParseCommandTellsCommandsFromMessagesThatOpenWithASlash(t *testing.T) {
	tests := []struct {
		text   string
		name   string
		params string
		ok     bool
	}{
		{"/leave", "leave", "", true},
		{"/leave ", "leave", "", true},
		{"  /leave  ", "leave", "", true},
		{"/invite @jane @bob", "invite", "@jane @bob", true},
		{"/topic release week", "topic", "release week", true},
		{"/LEAVE", "leave", "", true},
		{"/lenny-face", "lenny-face", "", true},

		// Things people type that merely start with a slash.
		{"/usr/bin/env is missing", "", "", false},
		{"/2:30 stand-up", "", "", false},
		{"//comment", "", "", false},
		{"", "", "", false},
		{"/", "", "", false},
		{"not /leave", "", "", false},
		// A multi-line message is a message, whatever its first word looks like.
		{"/leave\nactually never mind", "", "", false},
	}

	for _, tc := range tests {
		name, params, ok := model.ParseCommand(tc.text)
		if ok != tc.ok || name != tc.name || params != tc.params {
			t.Errorf("ParseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.text, name, params, ok, tc.name, tc.params, tc.ok)
		}
	}
}

func TestUsageRendersTheParamsHint(t *testing.T) {
	if got := (model.Command{Name: "exit"}).Usage(); got != "/exit" {
		t.Errorf("usage = %q", got)
	}
	if got := (model.Command{Name: "invite", Params: "@username"}).Usage(); got != "/invite @username" {
		t.Errorf("usage = %q", got)
	}
}

// Anything nobody can run must stay out of the completer: offering it would be
// offering a failure.
func TestUnsupportedCommandsAreNotOfferable(t *testing.T) {
	if (model.Command{Name: "giphy", Scope: model.ScopeUnsupported}).Offerable() {
		t.Error("an unsupported command should not be offered")
	}
	if !(model.Command{Name: "leave", Scope: model.ScopeLocal}).Offerable() {
		t.Error("a local command should be offered")
	}
}
