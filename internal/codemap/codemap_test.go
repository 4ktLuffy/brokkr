package codemap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const baseMod = `"""Models."""
LIMIT = 10


class Base:
    """The base class.

    More text.
    """

    def save(self, force=False):
        """Write it."""
        return helper(self)


class Child(Base):
    def save(self, force=False):
        return super().save(force)


def helper(obj, *, strict: bool = True) -> int:
    return 1
`

func fixture(t *testing.T) *Index {
	root := t.TempDir()
	write(t, root, "pkg/models.py", baseMod)
	write(t, root, "pkg/views.py", "from pkg.models import Child, helper\n\n\ndef view():\n    c = Child()\n    c.save()\n    # helper is only mentioned here\n    return helper(c), 'helper'\n")
	write(t, root, "tests/test_models.py", "from pkg.models import Base\n\n\ndef test_it():\n    Base().save()\n")
	write(t, root, "pkg/broken.py", "def (:\n")
	write(t, root, "node_modules/x.py", "def helper(): pass\n")
	ix := New(root)
	t.Cleanup(ix.Close)
	return ix
}

func TestFindDefinition(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	got := ix.Query("find_definition", "Base.save")
	if !strings.Contains(got, "pkg/models.py:11-13") || !strings.Contains(got, "def save(self, force=False)") || !strings.Contains(got, "-- Write it.") {
		t.Errorf("Base.save: %s", got)
	}
	if strings.Contains(got, "Child") {
		t.Errorf("a qualified name must not return other classes' methods: %s", got)
	}
	got = ix.Query("find_definition", "save")
	if !strings.Contains(got, "[in Base]") || !strings.Contains(got, "[in Child]") {
		t.Errorf("bare method name should list both: %s", got)
	}
	if got := ix.Query("find_definition", "helper"); !strings.Contains(got, "def helper(obj, *, strict: bool=True) -> int") {
		t.Errorf("function signature: %s", got)
	} else if strings.Contains(got, "node_modules") {
		t.Errorf("vendored directories must not be indexed: %s", got)
	}
	if got := ix.Query("find_definition", "pkg.models.helper"); !strings.Contains(got, "pkg/models.py:") {
		t.Errorf("module path: %s", got)
	}
	if got := ix.Query("find_definition", "LIMIT"); !strings.Contains(got, "variable LIMIT = 10") {
		t.Errorf("module variable: %s", got)
	}
	if got := ix.Query("find_definition", "Base"); !strings.Contains(got, "class Base") || !strings.Contains(got, "-- The base class.") {
		t.Errorf("class and docstring first line: %s", got)
	}
}

// Negative controls: a name that is not there gets a helpful message, not an
// empty answer, and a near miss is suggested.
func TestFindDefinitionUnknown(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	got := ix.Query("find_definition", "Basee")
	if !strings.Contains(got, "no class, function") || !strings.Contains(got, "Similar names: Base") {
		t.Errorf("near miss should be suggested: %s", got)
	}
	if got := ix.Query("find_definition", "zzzzqq"); !strings.Contains(got, "no class, function") || !strings.Contains(got, "search tool") || strings.Contains(got, "Similar") {
		t.Errorf("unknown name: %s", got)
	}
	if !strings.Contains(ix.Query("find_definition", "zzzzqq"), "1 files could not be parsed") {
		t.Errorf("unparsable files should be mentioned")
	}
	if got := ix.Query("find_definition", "  "); !strings.HasPrefix(got, "error:") {
		t.Errorf("empty name: %s", got)
	}
	if got := ix.Query("find_definition", "elp"); !strings.Contains(got, "helper") || !strings.Contains(got, "contain it") {
		t.Errorf("partial match: %s", got)
	}
}

func TestFindDefinitionCap(t *testing.T) {
	needPython(t)
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		write(t, root, filepath.Join("m", "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".py"), "def run(x):\n    pass\n")
	}
	ix := New(root)
	defer ix.Close()
	got := ix.Query("find_definition", "run")
	if n := strings.Count(got, "def run"); n != 15 || !strings.Contains(got, "40 definitions in total; 15 shown") {
		t.Errorf("%d shown: %s", n, got)
	}
}

func TestFindUsages(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	got := ix.Query("find_usages", "helper")
	for _, want := range []string{"pkg/views.py (", "[import] from pkg.models import Child, helper", "[call] return helper(c), 'helper'", "pkg/models.py ("} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Parsed references only: the comment and the string literal are not usages.
	if strings.Contains(got, "# helper is only mentioned") || strings.Count(got, "views.py (") != 1 {
		t.Errorf("comments are not usages:\n%s", got)
	}
	if strings.Contains(got, "  7:") {
		t.Errorf("the comment line was listed:\n%s", got)
	}
	// Methods: attribute calls found, and the other definitions of the name are
	// listed so the agent sees there is more than one place.
	got = ix.Query("find_usages", "save")
	if !strings.Contains(got, "[call] c.save()") || !strings.Contains(got, "[call] Base().save()") {
		t.Errorf("method calls:\n%s", got)
	}
	if !strings.Contains(got, "Also defined (2 places") || !strings.Contains(got, "Base.save") || !strings.Contains(got, "Child.save") {
		t.Errorf("other definitions of the name:\n%s", got)
	}
	// Library code before tests.
	if strings.Index(got, "tests/test_models.py") < strings.Index(got, "pkg/views.py") {
		t.Errorf("tests should come last:\n%s", got)
	}
	if got := ix.Query("find_usages", "Base.save"); !strings.Contains(got, "files that also mention Base come first") {
		t.Errorf("owner note:\n%s", got)
	}
	if got := ix.Query("find_usages", "strict"); !strings.Contains(got, "[kwarg]") && !strings.Contains(got, "defined") {
		t.Errorf("a parameter name that is never passed: %s", got)
	}
}

func TestFindUsagesUnknownAndUnused(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	if got := ix.Query("find_usages", "nosuchthing"); !strings.Contains(got, "no definition or reference of") {
		t.Errorf("unknown: %s", got)
	}
	if got := ix.Query("find_usages", "LIMIT"); !strings.Contains(got, "is defined (pkg/models.py:2") || !strings.Contains(got, "nothing in the indexed files references") {
		t.Errorf("defined but unused: %s", got)
	}
}

func TestOutline(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	got := ix.Query("outline", "pkg/models.py")
	for _, want := range []string{"class Base  -- The base class.", "  def save(self, force=False)  -- Write it.", "class Child(Base)", "def helper(obj", "module variables (1): LIMIT"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := ix.Query("outline", "./pkg/models.py"); !strings.Contains(got, "class Base") {
		t.Errorf("leading ./: %s", got)
	}
	if got := ix.Query("outline", "models.py"); !strings.HasPrefix(got, "error:") || !strings.Contains(got, "Did you mean: pkg/models.py") {
		t.Errorf("wrong path should suggest: %s", got)
	}
	if got := ix.Query("outline", "pkg/broken.py"); !strings.Contains(got, "could not be parsed") {
		t.Errorf("broken file: %s", got)
	}
	if got := ix.Query("outline", "README.md"); !strings.Contains(got, ".py files only") {
		t.Errorf("non-python: %s", got)
	}
}

// The agent edits files; the index must follow without a rebuild call.
func TestIndexFollowsEdits(t *testing.T) {
	needPython(t)
	ix := fixture(t)
	if got := ix.Query("find_definition", "brand_new"); !strings.Contains(got, "no class") {
		t.Fatalf("before the edit: %s", got)
	}
	write(t, ix.root, "pkg/models.py", baseMod+"\n\ndef brand_new():\n    return helper(None)\n")
	if got := ix.Query("find_definition", "brand_new"); !strings.Contains(got, "pkg/models.py:") {
		t.Errorf("edited file not re-indexed: %s", got)
	}
	write(t, ix.root, "pkg/extra.py", "def another(): return brand_new()\n")
	if got := ix.Query("find_usages", "brand_new"); !strings.Contains(got, "pkg/extra.py") {
		t.Errorf("new file not indexed: %s", got)
	}
	os.Remove(filepath.Join(ix.root, "pkg/extra.py"))
	if got := ix.Query("find_usages", "brand_new"); strings.Contains(got, "pkg/extra.py") {
		t.Errorf("deleted file still indexed: %s", got)
	}
}

func TestNoPython(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ix := New(t.TempDir())
	defer ix.Close()
	if got := ix.Query("outline", "a.py"); !strings.Contains(got, "no python3") {
		t.Errorf("missing interpreter should be said plainly: %s", got)
	}
	if got := ix.Check("a.py", t.TempDir()); got != "" {
		t.Errorf("check without python: %q", got)
	}
}

// ---- static check

func checkSrc(t *testing.T, before, after string) string {
	t.Helper()
	root, orig := t.TempDir(), t.TempDir()
	if before != "" {
		write(t, orig, "m.py", before)
	}
	write(t, root, "m.py", after)
	ix := New(root)
	defer ix.Close()
	return ix.Check("m.py", orig)
}

func TestCheckFindsHighConfidenceProblems(t *testing.T) {
	needPython(t)
	cases := map[string]struct{ src, want string }{
		"undefined":  {"def f():\n    return missing_name + 1\n", "undefined name 'missing_name'"},
		"before":     {"print(LATER)\nLATER = 1\n", "'LATER' is used before it is defined (first defined at line 2)"},
		"dup arg":    {"def f(a, b, a):\n    pass\n", "duplicate argument 'a' in f"},
		"return":     {"x = 1\nreturn x\n", "'return' outside function"},
		"break":      {"if True:\n    break\n", "'break' outside loop"},
		"class name": {"class A:\n    k = 1\n    def m(self):\n        return k\n", "undefined name 'k'"},
		"import use": {"x = path.join('a')\nimport os.path as path\n", "'path' is used before it is defined"},
	}
	for name, c := range cases {
		if got := checkSrc(t, "", c.src); !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// Negative controls: valid code, including the patterns a naive checker flags,
// must produce nothing.
func TestCheckStaysQuietOnValidCode(t *testing.T) {
	needPython(t)
	valid := map[string]string{
		"builtins and imports":            "import os\nfrom collections import defaultdict as dd\n\ndef f(x):\n    return len(os.sep) + int(x) + dd(list).__len__()\n",
		"defined later, used in function": "def f():\n    return g()\n\ndef g():\n    return 1\n",
		"class attribute in class body":   "class A:\n    k = 1\n    j = k + 1\n    def m(self):\n        return self.k, __class__\n",
		"comprehension":                   "xs = [i * 2 for i in range(3) if i]\nys = {k: v for k, v in zip(xs, xs)}\nz = sum(i for i in xs)\n",
		"global assigned in function":     "def setup():\n    global CONF\n    CONF = 1\n\ndef use():\n    return CONF\n",
		"nested closure":                  "def outer():\n    v = 1\n    def inner():\n        return v\n    return inner\n",
		"nonlocal":                        "def outer():\n    n = 0\n    def inner():\n        nonlocal n\n        n += 1\n    return inner\n",
		"try/except NameError":            "try:\n    legacy\nexcept NameError:\n    legacy = None\n",
		"conditional import fallback":     "try:\n    import json\nexcept ImportError:\n    json = None\nprint(json)\n",
		"with and except names":           "with open('f') as fh:\n    data = fh.read()\ntry:\n    pass\nexcept ValueError as err:\n    print(err)\n",
		"walrus":                          "if (n := 3) > 2:\n    print(n)\n[y for x in range(3) if (y := x)]\n",
		"lambda default":                  "k = 2\nf = lambda a, b=k: a + b\n",
		"loop reuse":                      "for i in range(3):\n    if i:\n        print(prev)\n    prev = i\n",
		"star import disables the check":  "from os.path import *\nprint(join('a'), mystery)\n",
		"globals() disables the check":    "globals()['magic'] = 1\nprint(magic)\n",
		"decorator and bases":             "import functools\n\n@functools.lru_cache()\ndef f(): pass\n\nclass B(dict): pass\nclass C(B, metaclass=type): pass\n",
		"annotations with future import":  "from __future__ import annotations\n\ndef f(x: Later) -> Later: ...\n\nclass Later: ...\n",
		"for else and while":              "while True:\n    break\nelse:\n    pass\nfor _ in []:\n    continue\n",
		"del and star args":               "def f(*a, **k):\n    x = 1\n    del x\n    return a, k\n",
		"__file__ and __name__":           "print(__file__, __name__, __doc__)\n",
		"python 2 name behind a guard":    "import sys\nif sys.version_info[0] == 2:\n    text = unicode\n",
	}
	for name, src := range valid {
		if got := checkSrc(t, "", src); got != "" {
			t.Errorf("%s: false positive %q", name, got)
		}
	}
}

// Only what the edit introduced is reported: a file that already had the
// problem does not nag, a file that gains one does.
func TestCheckReportsOnlyWhatTheEditIntroduced(t *testing.T) {
	needPython(t)
	old := "def f():\n    return old_bug\n"
	if got := checkSrc(t, old, old+"\ndef g():\n    return 1\n"); got != "" {
		t.Errorf("pre-existing problem reported: %q", got)
	}
	if got := checkSrc(t, old, old+"\ndef g():\n    return new_bug\n"); !strings.Contains(got, "new_bug") || strings.Contains(got, "old_bug") {
		t.Errorf("new problem: %q", got)
	}
	// A new file has nothing to compare with.
	if got := checkSrc(t, "", old); !strings.Contains(got, "old_bug") {
		t.Errorf("new file: %q", got)
	}
	// A file that does not parse is the syntax check's business.
	if got := checkSrc(t, "", "def (:\n"); got != "" {
		t.Errorf("syntax error reported by the wrong check: %q", got)
	}
}
