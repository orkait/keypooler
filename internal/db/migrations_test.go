package db

import "testing"

func TestAMigrationFileNamesItsVersionThenItsName(t *testing.T) {
	cases := map[string]struct {
		version int
		name    string
		valid   bool
	}{
		"001_init.sql":     {1, "init", true},
		"12_add_index.sql": {12, "add_index", true},
		"init.sql":         {valid: false},
		"1x_init.sql":      {valid: false},
		"001_.sql":         {valid: false},
		"_init.sql":        {valid: false},
	}
	for filename, c := range cases {
		t.Run(filename, func(t *testing.T) {
			version, name, err := parseMigrationName(filename)
			if (err == nil) != c.valid || version != c.version || name != c.name {
				t.Fatalf("got %d %q err %v, want %d %q valid %v", version, name, err, c.version, c.name, c.valid)
			}
		})
	}
}
