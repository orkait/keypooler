package api

import "testing"

func TestExtractPathParamTakesTheSegmentAfterThePrefix(t *testing.T) {
	cases := []struct{ path, prefix, want string }{
		{"/admin/keys/k1", "/admin/keys/", "k1"},
		{"/key/k1/exhausted", "/key/", "k1"},
		{"/admin/keys/", "/admin/keys/", ""},
	}
	for _, c := range cases {
		if got := extractPathParam(c.path, c.prefix); got != c.want {
			t.Errorf("extractPathParam(%q, %q) = %q, want %q", c.path, c.prefix, got, c.want)
		}
	}
}
