package config

import (
	"errors"
	"testing"
)

func TestADeployNeedsAnAdminTokenAndAPostgresURL(t *testing.T) {
	cases := map[string]struct {
		token, url string
		want       error
	}{
		"complete":     {"t", "postgres://u@h/db", nil},
		"postgresql":   {"t", "postgresql://u@h/db", nil},
		"no-token":     {"", "postgres://u@h/db", ErrNoAdminToken},
		"not-postgres": {"t", "libsql://h", ErrDatabaseURL},
		"no-database":  {"t", "", ErrDatabaseURL},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envAdminToken, c.token)
			t.Setenv(envDatabaseURL, c.url)
			if _, err := Load(); !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
		})
	}
}
