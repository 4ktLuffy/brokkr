package verify

import "strings"

// pytestStatuses are the -rA summary prefixes SWE-bench's TestStatus knows.
var pytestStatuses = []string{"FAILED", "PASSED", "SKIPPED", "ERROR", "XFAIL"}

// parsePytest ports parse_log_pytest from SWE-bench v4.1.0
// (swebench/harness/log_parsers/python.py) for `pytest -rA` output: a line
// starting with a status is "STATUS test_id [...]". As in SWE-bench's grading,
// PASSED and XFAIL count as passing; FAILED and ERROR as failing.
func parsePytest(log string) (passed, failed map[string]bool) {
	status := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		matched := false
		for _, s := range pytestStatuses {
			if strings.HasPrefix(line, s) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if strings.HasPrefix(line, "FAILED") {
			line = strings.ReplaceAll(line, " - ", " ")
		}
		f := strings.Fields(line)
		if len(f) <= 1 {
			continue
		}
		status[f[1]] = f[0]
	}
	passed, failed = map[string]bool{}, map[string]bool{}
	for name, st := range status {
		switch st {
		case "PASSED", "XFAIL":
			passed[name] = true
		case "FAILED", "ERROR":
			failed[name] = true
		}
	}
	return passed, failed
}
