package verify

import (
	"regexp"
	"strings"
)

// sympyFailureHeader matches SymPy's failure banners, e.g.
// "____ sympy/core/tests/test_basic.py:test_foo ____".
var sympyFailureHeader = regexp.MustCompile(`(_*) (.*)\.py:(.*) (_*)`)

// parseSympy ports parse_log_sympy from SWE-bench v4.1.0
// (swebench/harness/log_parsers/python.py) for bin/test --verbose output:
// "test_name ok", "test_name F", "test_name E", plus failure banners. As in
// the original, a later status for a name overwrites an earlier one.
func parseSympy(log string) (passed, failed map[string]bool) {
	status := map[string]string{}
	for _, m := range sympyFailureHeader.FindAllStringSubmatch(log, -1) {
		status[m[2]+".py:"+m[3]] = "FAILED"
	}
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "test_") {
			continue
		}
		name := strings.Fields(line)[0]
		switch {
		case strings.HasSuffix(line, " E"):
			status[name] = "ERROR"
		case strings.HasSuffix(line, " F"):
			status[name] = "FAILED"
		case strings.HasSuffix(line, " ok"):
			status[name] = "PASSED"
		}
	}
	passed, failed = map[string]bool{}, map[string]bool{}
	for name, st := range status {
		if st == "PASSED" {
			passed[name] = true
		} else {
			failed[name] = true
		}
	}
	return passed, failed
}
