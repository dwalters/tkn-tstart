package envsubst

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

// varPattern matches ${...} and $VAR forms.
var varPattern = regexp.MustCompile(`\$(?:\{([^}]+)\}|([A-Za-z_][A-Za-z0-9_]*))`)

// Expand performs bash-style envsubst, supporting the operators drone/flux support:
//
//	${VAR}           simple substitution
//	${VAR:-default}  use default if VAR is unset or empty
//	${VAR-default}   use default if VAR is unset
//	${VAR:=default}  assign & use default if VAR is unset or empty
//	${VAR=default}   assign & use default if VAR is unset
//	${VAR:+alt}      use alt if VAR is set and non-empty
//	${VAR+alt}       use alt if VAR is set
//	${VAR:?msg}      error if VAR is unset or empty
//	${VAR?msg}       error if VAR is unset
//	${#VAR}          length of VAR
//	${VAR#pattern}   strip shortest prefix matching pattern
//	${VAR##pattern}  strip longest prefix matching pattern
//	${VAR%pattern}   strip shortest suffix matching pattern
//	${VAR%%pattern}  strip longest suffix matching pattern
//	$VAR             simple substitution (no braces)
func Expand(s string) string {
	expanded, _ := expand(s, false)
	return expanded
}

// ExpandStrict is like Expand but returns an error for ${VAR:?msg} / ${VAR?msg}.
func ExpandStrict(s string) (string, error) {
	return expand(s, true)
}

func expand(s string, strict bool) (string, error) {
	var retErr error
	result := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		if retErr != nil {
			return match
		}

		subs := varPattern.FindStringSubmatch(match)
		braced := subs[1]
		bare := subs[2]

		if bare != "" {
			val, _ := os.LookupEnv(bare)
			return val
		}

		// Handle ${#VAR} — string length.
		if strings.HasPrefix(braced, "#") {
			name := braced[1:]
			val, _ := os.LookupEnv(name)
			return fmt.Sprintf("%d", len(val))
		}

		// Parse operator: ##, #, %%, %, :-, :=, :+, :?, -, =, +, ?
		name, op, operand := parseExpr(braced)
		val, set := os.LookupEnv(name)

		switch op {
		case "":
			return val

		case ":-":
			if !set || val == "" {
				return operand
			}
			return val

		case "-":
			if !set {
				return operand
			}
			return val

		case ":=":
			if !set || val == "" {
				os.Setenv(name, operand) //nolint:errcheck
				return operand
			}
			return val

		case "=":
			if !set {
				os.Setenv(name, operand) //nolint:errcheck
				return operand
			}
			return val

		case ":+":
			if set && val != "" {
				return operand
			}
			return ""

		case "+":
			if set {
				return operand
			}
			return ""

		case ":?":
			if !set || val == "" {
				msg := operand
				if msg == "" {
					msg = fmt.Sprintf("parameter '%s' is unset or empty", name)
				}
				if strict {
					retErr = fmt.Errorf("%s", msg)
				}
				return ""
			}
			return val

		case "?":
			if !set {
				msg := operand
				if msg == "" {
					msg = fmt.Sprintf("parameter '%s' is unset", name)
				}
				if strict {
					retErr = fmt.Errorf("%s", msg)
				}
				return ""
			}
			return val

		case "##":
			return stripPrefix(val, operand, true)
		case "#":
			return stripPrefix(val, operand, false)
		case "%%":
			return stripSuffix(val, operand, true)
		case "%":
			return stripSuffix(val, operand, false)
		}

		return val
	})

	return result, retErr
}

// parseExpr splits a braced expression like VAR:-default into (name, op, operand).
func parseExpr(expr string) (name, op, operand string) {
	// Two-character operators that start with ':'
	for _, o := range []string{":-", ":=", ":+", ":?"} {
		if idx := strings.Index(expr, o); idx >= 0 {
			return expr[:idx], o, expr[idx+len(o):]
		}
	}

	// Double-char prefix/suffix operators
	for _, o := range []string{"##", "%%"} {
		if idx := strings.Index(expr, o); idx >= 0 {
			return expr[:idx], o, expr[idx+2:]
		}
	}

	// Single-char operators
	for _, o := range []string{"-", "=", "+", "?", "#", "%"} {
		if idx := strings.Index(expr, o); idx >= 0 {
			return expr[:idx], o, expr[idx+1:]
		}
	}

	return expr, "", ""
}

// stripPrefix strips a glob prefix from s; longest=true uses ## semantics.
func stripPrefix(s, pattern string, longest bool) string {
	if pattern == "" {
		return s
	}
	if longest {
		// Try longest match by walking the string.
		for i := len(s); i >= 0; i-- {
			matched, _ := path.Match(pattern, s[:i])
			if matched {
				return s[i:]
			}
		}
		return s
	}
	// Shortest: try from index 0 upward.
	for i := 0; i <= len(s); i++ {
		matched, _ := path.Match(pattern, s[:i])
		if matched {
			return s[i:]
		}
	}
	return s
}

// stripSuffix strips a glob suffix from s; longest=true uses %% semantics.
func stripSuffix(s, pattern string, longest bool) string {
	if pattern == "" {
		return s
	}
	if longest {
		for i := 0; i <= len(s); i++ {
			matched, _ := path.Match(pattern, s[i:])
			if matched {
				return s[:i]
			}
		}
		return s
	}
	for i := len(s); i >= 0; i-- {
		matched, _ := path.Match(pattern, s[i:])
		if matched {
			return s[:i]
		}
	}
	return s
}
