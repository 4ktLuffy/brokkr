package verify

import (
	"regexp"
	"strings"
)

// Parsers for repositories that are not Python, ported from SWE-bench
// (swebench/harness/log_parsers/{go,rust,c}.py at commit 02e7a74ffd0b, the
// parsers SWE-bench Multilingual is graded with). Each original matches one
// regular expression against every stripped line and stores the status in a
// dict, so a later line for the same test overwrites an earlier one; statusMap
// keeps that. Skipped tests are neither passed nor failed, as in SWE-bench's
// grading (only PASSED and XFAIL count as passing).

type lineRule struct {
	re *regexp.Regexp
	// status maps the matched groups to (name, status); status "" ignores
	// the line.
	status func(m []string) (string, string)
}

var multilangParsers = map[string]lineRule{
	// parse_log_gotest: "--- PASS: TestName (0.01s)", subtests included.
	"gotest": {regexp.MustCompile(`^--- (PASS|FAIL|SKIP): (.+) \((.+)\)$`), func(m []string) (string, string) {
		return m[2], map[string]string{"PASS": "PASSED", "FAIL": "FAILED", "SKIP": "SKIPPED"}[m[1]]
	}},
	// parse_log_cargo: "test path::name ... ok".
	"cargo": {regexp.MustCompile(`^test\s+(\S+)\s+\.\.\.\s+(\w+)$`), func(m []string) (string, string) {
		return m[1], map[string]string{"ok": "PASSED", "FAILED": "FAILED"}[m[2]]
	}},
	// parse_log_googletest: "[       OK ] Suite.Name (0 ms)".
	"googletest": {regexp.MustCompile(`^.*\[\s*(OK|FAILED)\s*\]\s(.*)\s\(.*\)$`), func(m []string) (string, string) {
		return m[2], map[string]string{"OK": "PASSED", "FAILED": "FAILED"}[m[1]]
	}},
	// parse_log_jq: "PASS: tests/jqtest".
	"jq": {regexp.MustCompile(`^\s*(PASS|FAIL):\s(.+)$`), func(m []string) (string, string) {
		return m[2], map[string]string{"PASS": "PASSED", "FAIL": "FAILED"}[m[1]]
	}},
	// parse_log_redis (also valkey): "[ok]: name (12 ms)"; a failed name loses
	// its trailing "in <file>".
	"redis": {regexp.MustCompile(`^\[(ok|err|skip|ignore)\]:\s(.+?)(?:\s\((\d+\s*m?s)\))?$`), func(m []string) (string, string) {
		switch m[1] {
		case "ok":
			return m[2], "PASSED"
		case "err":
			return redisInFile.ReplaceAllString(m[2], ""), "FAILED"
		}
		return m[2], "SKIPPED"
	}},
	// parse_log_micropython_test: "pass  basics/int_big.py".
	"micropython": {regexp.MustCompile(`^(pass|FAIL|skip)\s+(.+)$`), func(m []string) (string, string) {
		return m[2], map[string]string{"pass": "PASSED", "FAIL": "FAILED", "skip": "SKIPPED"}[m[1]]
	}},
}

var redisInFile = regexp.MustCompile(`\s+in\s+\S+$`)

// parseMultilang runs the named parser; ok is false for an unknown name.
func parseMultilang(name, log string) (passed, failed map[string]bool, ok bool) {
	rule, ok := multilangParsers[name]
	if !ok {
		return nil, nil, false
	}
	status := map[string]string{}
	for _, raw := range strings.Split(log, "\n") {
		m := rule.re.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		if test, st := rule.status(m); st != "" {
			status[test] = st
		}
	}
	passed, failed = map[string]bool{}, map[string]bool{}
	for test, st := range status {
		switch st {
		case "PASSED":
			passed[test] = true
		case "FAILED":
			failed[test] = true
		}
	}
	return passed, failed, true
}
