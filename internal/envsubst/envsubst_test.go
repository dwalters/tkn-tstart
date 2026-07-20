package envsubst

import (
	"os"
	"testing"
)

func TestExpand(t *testing.T) {
	os.Setenv("SET_VAR", "hello")
	os.Setenv("EMPTY_VAR", "")
	os.Unsetenv("UNSET_VAR")

	cases := []struct {
		in   string
		want string
	}{
		{"$SET_VAR", "hello"},
		{"${SET_VAR}", "hello"},
		{"${UNSET_VAR}", ""},

		// :-
		{"${SET_VAR:-fallback}", "hello"},
		{"${EMPTY_VAR:-fallback}", "fallback"},
		{"${UNSET_VAR:-fallback}", "fallback"},

		// -
		{"${SET_VAR-fallback}", "hello"},
		{"${EMPTY_VAR-fallback}", ""},
		{"${UNSET_VAR-fallback}", "fallback"},

		// :+
		{"${SET_VAR:+alt}", "alt"},
		{"${EMPTY_VAR:+alt}", ""},
		{"${UNSET_VAR:+alt}", ""},

		// +
		{"${SET_VAR+alt}", "alt"},
		{"${EMPTY_VAR+alt}", "alt"},
		{"${UNSET_VAR+alt}", ""},

		// #length
		{"${#SET_VAR}", "5"},
		{"${#UNSET_VAR}", "0"},

		// # prefix strip
		{"${SET_VAR#hel}", "lo"},
		{"${SET_VAR#xyz}", "hello"},

		// % suffix strip
		{"${SET_VAR%lo}", "hel"},
		{"${SET_VAR%xyz}", "hello"},

		// composed
		{"prefix-${SET_VAR}-suffix", "prefix-hello-suffix"},
	}

	for _, c := range cases {
		got := Expand(c.in)
		if got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandStrict_Error(t *testing.T) {
	os.Unsetenv("UNSET_VAR")
	_, err := ExpandStrict("${UNSET_VAR:?must be set}")
	if err == nil {
		t.Fatal("expected error for :? with unset var")
	}
}
