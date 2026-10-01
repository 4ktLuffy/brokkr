package agent

import (
	"fmt"
	"strings"

	"github.com/4ktLuffy/brokkr/internal/model"
)

// Code navigation tools and the post-edit static check, enabled by
// Config.CodeTools (off by default; see internal/codemap). Failures in the
// agent runs they target: 43% of failed runs never edited, 47 read and searched
// until the turns ran out, and multi-file fixes solved 1 of 39 (results/ANALYSIS.md).

// codeToolsPrompt is appended to the system prompt only when the tools are on.
const codeToolsPrompt = `

You also have code navigation tools for Python. find_definition(name) gives file, line range and signature of a class, function, method (Class.method) or module-level name. find_usages(name) lists where a name is called or referenced and where else the same name is defined: use it before and after you change something, because a fix often needs the same change in other places (overrides, callers, copies). outline(path) lists a file's classes and functions with line ranges: use it, then read_file only the range you need. After an edit to a .py file you are warned about names that are undefined or used before they are defined.`

// withCodeTools adds the three tools after read_file, next to the other
// read-only tools.
func withCodeTools(tools []model.Tool) []model.Tool {
	s := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	t := func(name, desc string, props map[string]any, req string) model.Tool {
		return model.Tool{Type: "function", Function: model.ToolFunction{Name: name, Description: desc,
			Parameters: map[string]any{"type": "object", "properties": props, "required": []string{req}}}}
	}
	extra := []model.Tool{
		t("find_definition", "Find where a Python class, function, method or module-level name is defined. Returns file:lines, signature and the first docstring line. Accepts Class.method and package.module.name; partial names work if there is no exact match.",
			map[string]any{"name": s("e.g. 'QuerySet.filter', 'parse_header', 'django.db.models.Q'")}, "name"),
		t("find_usages", "Find where a name is used in the Python code: calls, attribute access, imports, assignments (parsed, not text search). Grouped by file with the source line. Also lists other definitions with the same name, which often need the same fix.",
			map[string]any{"name": s("function, class, method or variable name; 'Class.method' prefers files that mention Class")}, "name"),
		t("outline", "List the classes, functions and methods of one Python file with line ranges and signatures. Cheaper than reading the file.",
			map[string]any{"path": s("path relative to the repository root")}, "path"),
	}
	at := 3 // after list_dir, search, read_file
	for i, tl := range tools {
		if tl.Function.Name == "read_file" {
			at = i + 1
		}
	}
	out := append([]model.Tool{}, tools[:at]...)
	out = append(out, extra...)
	return append(out, tools[at:]...)
}

// codeCall answers a code tool. Without the flag the tools do not exist, so a
// call is refused like any unknown tool.
func (w *workspace) codeCall(name string, args map[string]any) (string, error) {
	if w.code == nil {
		return "", fmt.Errorf("%w: unknown tool %q", errRefused, name)
	}
	arg, _ := args["name"].(string)
	if name == "outline" {
		arg, _ = args["path"].(string)
	}
	return w.code.Query(name, arg), nil
}

// staticCheck reports problems the file at p has now that it did not have
// before the edit, or "" (see internal/codemap/check.py for what it covers).
func (w *workspace) staticCheck(p string) string {
	if w.code == nil || !strings.HasSuffix(p, ".py") {
		return ""
	}
	_, rel, err := w.resolve(p)
	if err != nil {
		return ""
	}
	return w.code.Check(rel, w.orig)
}
