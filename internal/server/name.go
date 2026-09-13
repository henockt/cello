package server

import (
	"crypto/rand"
	"fmt"
	"strings"
)

const (
	// nameAlphabet is 32 characters with the lookalikes (l, 1, o, 0) removed
	nameAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

	// length of client channel names
	nameLen = 10

	// retries when a generated name is already taken
	nameAttempts = 5

	// DNS label limit, a channel name becomes a hostname label
	maxNameLen = 63
)

// newChannelName returns a random name usable as a DNS label.
func newChannelName() (string, error) {
	b := make([]byte, nameLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = nameAlphabet[b[i]&31]
	}
	return string(b), nil
}

// validateChannelName reports whether name is usable as a hostname label.
// the error is shown to the user, so it says what is allowed.
func validateChannelName(name string) error {
	if name == "" {
		return fmt.Errorf("channel name must not be empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("channel name must be at most %d characters", maxNameLen)
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("channel name must not start or end with a hyphen")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("channel name may only contain lowercase letters, digits and hyphens")
		}
	}
	return nil
}
