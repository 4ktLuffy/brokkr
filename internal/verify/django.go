package verify

import (
	"regexp"
	"strings"
)

// parseDjango ports parse_log_django from SWE-bench v4.1.0
// (swebench/harness/log_parsers/python.py). A test's name is the text before
// " ... " on its line, which for a test with a docstring is the docstring's
// first line: SWE-bench's FAIL_TO_PASS and PASS_TO_PASS lists use exactly these
// names, so matching its parser matters more than improving on it. Later
// statuses for a name overwrite earlier ones, as in the original.
func parseDjango(log string) (passed, failed map[string]bool) {
	status := map[string]string{}
	var prev string
	havePrev := false
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "--version is equivalent to version") {
			status["--version is equivalent to version"] = "PASSED"
		}
		if strings.Contains(line, " ... ") {
			prev, havePrev = strings.SplitN(line, " ... ", 2)[0], true
		}
		for _, suf := range []string{" ... ok", " ... OK", " ...  OK"} {
			if strings.HasSuffix(line, suf) {
				if strings.HasPrefix(line, "Applying sites.0002_alter_domain_unique...test_no_migrations") {
					parts := strings.SplitN(line, "...", 2)
					line = strings.TrimSpace(parts[len(parts)-1])
				}
				status[line[:strings.LastIndex(line, suf)]] = "PASSED"
				break
			}
		}
		if i := strings.Index(line, " ... skipped"); i >= 0 {
			status[line[:i]] = "SKIPPED"
		}
		if strings.HasSuffix(line, " ... FAIL") {
			status[line[:strings.Index(line, " ... FAIL")]] = "FAILED"
		}
		if strings.HasPrefix(line, "FAIL:") {
			if f := strings.Fields(line); len(f) > 1 {
				status[f[1]] = "FAILED"
			}
		}
		if strings.HasSuffix(line, " ... ERROR") {
			status[line[:strings.Index(line, " ... ERROR")]] = "ERROR"
		}
		if strings.HasPrefix(line, "ERROR:") {
			if f := strings.Fields(line); len(f) > 1 {
				status[f[1]] = "ERROR"
			}
		}
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "ok") && havePrev {
			status[prev] = "PASSED"
		}
	}
	for _, re := range djangoInterrupted {
		for _, m := range re.FindAllStringSubmatch(log, -1) {
			status[m[1]] = "PASSED"
		}
	}
	passed, failed = map[string]bool{}, map[string]bool{}
	for name, st := range status {
		switch st {
		case "PASSED":
			passed[name] = true
		case "FAILED", "ERROR":
			failed[name] = true
		}
	}
	return passed, failed
}

// Django's logger sometimes interrupts a test line with a multi-line message
// before the "ok"; SWE-bench recognises these three forms.
var djangoInterrupted = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sTesting\ against\ Django\ installed\ in\ ((?s:.*?))\ silenced\)\.\nok$`),
	regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sInternal\ Server\ Error:\ \/(.*)\/\nok$`),
	regexp.MustCompile(`(?m)^(.*?)\s\.\.\.\sSystem check identified no issues \(0 silenced\)\nok$`),
}
