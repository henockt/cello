package server

import (
	"strings"
	"testing"
)

func TestNewChannelName(t *testing.T) {
	const iterations = 5000
	seen := make(map[string]bool, iterations)

	for range iterations {
		name, err := newChannelName()
		if err != nil {
			t.Fatalf("newChannelName() failed: %v", err)
		}
		if len(name) != nameLen {
			t.Fatalf("newChannelName() = %q, want %d characters", name, nameLen)
		}
		// A generated name becomes a hostname label, so it must survive the
		// same validation a client-supplied name does.
		if err := validateChannelName(name); err != nil {
			t.Fatalf("newChannelName() = %q, which is not a valid name: %v", name, err)
		}
		for _, r := range name {
			if !strings.ContainsRune(nameAlphabet, r) {
				t.Fatalf("newChannelName() = %q, contains %q which is outside the alphabet", name, r)
			}
		}
		if seen[name] {
			t.Fatalf("newChannelName() returned %q twice in %d draws", name, iterations)
		}
		seen[name] = true
	}
}

func TestNewChannelNameCannotCollideWithReservedWords(t *testing.T) {
	// Generated names are a fixed length no reserved word shares, which is why
	// the reserved list is only consulted for client-supplied names.
	for _, word := range []string{"www", "api", "admin", "mail"} {
		if len(word) == nameLen {
			t.Errorf("reserved word %q is %d characters, so a generated name could collide with it", word, nameLen)
		}
	}
}

func TestValidateChannelName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"simple", "myapp", false},
		{"with digits", "app2", false},
		{"with hyphen", "my-app", false},
		{"single character", "a", false},
		{"max length", strings.Repeat("a", maxNameLen), false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", maxNameLen+1), true},
		{"uppercase", "MyApp", true},
		{"leading hyphen", "-app", true},
		{"trailing hyphen", "app-", true},
		{"dot", "my.app", true},
		{"underscore", "my_app", true},
		{"space", "my app", true},
		{"non-ascii", "café", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChannelName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateChannelName(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
		})
	}
}
