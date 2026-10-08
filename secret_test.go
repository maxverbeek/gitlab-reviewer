package main

import "testing"

func TestExactAttrs(t *testing.T) {
	want := map[string]string{"service": "gitlab", "host": "gitlab.com"}
	cases := []struct {
		name string
		got  map[string]string
		ok   bool
	}{
		{"exact", map[string]string{"service": "gitlab", "host": "gitlab.com"}, true},
		{"schema", map[string]string{"service": "gitlab", "host": "gitlab.com", "xdg:schema": "org.freedesktop.Secret.Generic"}, true},
		{"extra", map[string]string{"service": "gitlab", "host": "gitlab.com", "user": "x"}, false},
		{"wrong host", map[string]string{"service": "gitlab", "host": "other"}, false},
		{"missing", map[string]string{"service": "gitlab"}, false},
	}
	for _, c := range cases {
		if got := exactAttrs(c.got, want); got != c.ok {
			t.Errorf("%s: got %v, want %v", c.name, got, c.ok)
		}
	}
}
