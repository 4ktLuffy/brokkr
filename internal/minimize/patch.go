package minimize

import (
	"fmt"
	"strconv"
	"strings"
)

// A patch is cut into units that can be dropped independently: each hunk of a
// modified file, and each new, deleted, renamed, mode-changed or binary file as
// a whole. In line mode the changed lines of the surviving hunks are units too.

type pline struct {
	kind   byte   // ' ', '-' or '+'
	text   string // the whole line including its newline, prefix included
	marker string // a following "\ No newline at end of file" line, if any
}

type hunk struct {
	oldStart, oldCount, newCount int
	head                         string // the original "@@ ... @@ section" line
	lines                        []pline
}

type file struct {
	path   string
	header []string
	hunks  []*hunk
	whole  bool // not splittable into hunks
}

// unit names one droppable piece: hunk h of file f, or the whole file when h
// is -1.
type unit struct{ f, h int }

type parsed struct {
	files []*file
	units []unit
}

// lineUnit names one changed line: line l of hunk unit u.
type lineUnit struct{ u, l int }

func parseHunkHead(s string) (oldStart, oldCount, newCount int, err error) {
	// @@ -a[,b] +c[,d] @@; a missing count means 1.
	f := strings.Fields(s)
	if len(f) < 3 || f[0] != "@@" || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, 0, fmt.Errorf("bad hunk header %q", strings.TrimSpace(s))
	}
	pair := func(r string) (start, count int, err error) {
		a, b, ok := strings.Cut(r, ",")
		count = 1
		if ok {
			if count, err = strconv.Atoi(b); err != nil {
				return 0, 0, err
			}
		}
		start, err = strconv.Atoi(a)
		return start, count, err
	}
	oldStart, oldCount, e1 := pair(f[1][1:])
	_, newCount, e2 := pair(f[2][1:])
	if e1 != nil || e2 != nil {
		return 0, 0, 0, fmt.Errorf("bad hunk header %q", strings.TrimSpace(s))
	}
	return oldStart, oldCount, newCount, nil
}

func diffPath(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	p := f[0]
	if p == "/dev/null" {
		return p
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	return p
}

// parse reads a unified diff (git style or diff -u).
func parse(text string) (*parsed, error) {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	p := &parsed{}
	var cur *file
	var h *hunk
	oldLeft, newLeft := 0, 0
	inHunk := func() bool { return h != nil && (oldLeft > 0 || newLeft > 0) }
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		if inHunk() {
			var k byte
			switch {
			case strings.HasPrefix(line, "-"):
				k = '-'
				oldLeft--
			case strings.HasPrefix(line, "+"):
				k = '+'
				newLeft--
			case strings.HasPrefix(line, " "), line == "\n":
				k = ' '
				oldLeft--
				newLeft--
			case strings.HasPrefix(line, `\`):
				if n := len(h.lines); n > 0 {
					h.lines[n-1].marker += line
				}
				continue
			default:
				return nil, fmt.Errorf("hunk of %s ends early at %q", cur.path, strings.TrimSpace(line))
			}
			h.lines = append(h.lines, pline{kind: k, text: line})
			continue
		}
		if h != nil && strings.HasPrefix(line, `\`) {
			if n := len(h.lines); n > 0 {
				h.lines[n-1].marker += line
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff "),
			strings.HasPrefix(line, "--- ") && (cur == nil || (len(cur.hunks) > 0 && !inHunk())):
			cur = &file{}
			p.files = append(p.files, cur)
			h = nil
			cur.header = append(cur.header, line)
			if strings.HasPrefix(line, "--- ") {
				cur.path = diffPath(line[4:])
			}
		case cur != nil && strings.HasPrefix(line, "@@ "):
			os_, oc, nc, err := parseHunkHead(line)
			if err != nil {
				return nil, err
			}
			h = &hunk{oldStart: os_, oldCount: oc, newCount: nc, head: line}
			oldLeft, newLeft = oc, nc
			cur.hunks = append(cur.hunks, h)
		case cur != nil && len(cur.hunks) == 0:
			cur.header = append(cur.header, line)
			switch {
			case strings.HasPrefix(line, "+++ "):
				if pp := diffPath(line[4:]); pp != "/dev/null" || cur.path == "" {
					cur.path = pp
				}
			case strings.HasPrefix(line, "--- ") && cur.path == "":
				cur.path = diffPath(line[4:])
			}
		}
		// Anything after the last hunk that starts no file (a format-patch
		// signature) is dropped; it plays no part in applying the patch.
	}
	if len(p.files) == 0 {
		return nil, fmt.Errorf("no file diffs found in patch")
	}
	for fi, f := range p.files {
		for _, l := range f.header {
			for _, pre := range []string{"new file mode", "deleted file mode", "rename ", "copy ", "old mode", "new mode", "similarity index", "GIT binary patch", "Binary files"} {
				if strings.HasPrefix(l, pre) {
					f.whole = true
				}
			}
			if l == "--- /dev/null\n" || l == "+++ /dev/null\n" {
				f.whole = true
			}
		}
		if len(f.hunks) == 0 {
			f.whole = true
		}
		if f.path == "" {
			f.path = fmt.Sprintf("file#%d", fi)
		}
		if f.whole {
			p.units = append(p.units, unit{fi, -1})
		} else {
			for hi := range f.hunks {
				p.units = append(p.units, unit{fi, hi})
			}
		}
	}
	return p, nil
}

// whole-file text, as parsed.
func (f *file) full() string {
	var b strings.Builder
	for _, l := range f.header {
		b.WriteString(l)
	}
	for _, h := range f.hunks {
		b.WriteString(h.head)
		for _, l := range h.lines {
			b.WriteString(l.text)
			b.WriteString(l.marker)
		}
	}
	return b.String()
}

// changeUnits lists the changed lines of the hunk units in keep that can be
// dropped on their own. A line followed by a "no newline" marker cannot: turning
// it into context would change what the marker says.
func (p *parsed) changeUnits(keep map[int]bool) []lineUnit {
	var out []lineUnit
	for ui, u := range p.units {
		if u.h < 0 || !keep[ui] {
			continue
		}
		for li, l := range p.files[u.f].hunks[u.h].lines {
			if l.kind != ' ' && l.marker == "" {
				out = append(out, lineUnit{ui, li})
			}
		}
	}
	return out
}

// render builds the patch from the kept units; dropLines are changed lines left
// out of kept hunks (an added line is omitted, a removed line becomes context).
func (p *parsed) render(keep map[int]bool, dropLines map[lineUnit]bool) string {
	var b strings.Builder
	ui := 0
	for fi, f := range p.files {
		var body strings.Builder
		delta := 0
		if f.whole {
			if keep[ui] {
				body.WriteString(f.full())
			}
			ui++
		} else {
			for hi, h := range f.hunks {
				u := ui
				ui++
				if !keep[u] {
					continue
				}
				var lines strings.Builder
				o, n, changes := 0, 0, 0
				for li, l := range h.lines {
					drop := dropLines[lineUnit{u, li}]
					switch l.kind {
					case ' ':
						o++
						n++
						lines.WriteString(l.text + l.marker)
					case '-':
						if drop {
							o++
							n++
							lines.WriteString(" " + l.text[1:])
						} else {
							o++
							changes++
							lines.WriteString(l.text + l.marker)
						}
					case '+':
						if !drop {
							n++
							changes++
							lines.WriteString(l.text + l.marker)
						}
					}
				}
				_ = hi
				if changes == 0 {
					continue
				}
				ns := h.oldStart + delta
				switch {
				case o == 0:
					ns++
				case n == 0:
					ns--
				}
				delta += n - o
				fmt.Fprintf(&body, "@@ -%s +%s @@%s", span(h.oldStart, o), span(ns, n), sectionOf(h.head))
				body.WriteString(lines.String())
			}
		}
		if body.Len() > 0 {
			if !f.whole {
				for _, l := range f.header {
					b.WriteString(l)
				}
			}
			b.WriteString(body.String())
		}
		_ = fi
	}
	return b.String()
}

func span(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// sectionOf returns what follows the closing @@ of a hunk header, newline included.
func sectionOf(head string) string {
	i := strings.Index(head[2:], "@@")
	if i < 0 {
		return "\n"
	}
	return head[2+i+2:]
}

// changed counts the added and removed lines of a patch.
func changed(text string) int {
	p, err := parse(text)
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range p.files {
		for _, h := range f.hunks {
			for _, l := range h.lines {
				if l.kind != ' ' {
					n++
				}
			}
		}
	}
	return n
}

func (p *parsed) describe(u unit) string {
	f := p.files[u.f]
	if u.h < 0 {
		return f.path + " (whole file)"
	}
	return f.path + " " + strings.TrimSpace(strings.SplitN(f.hunks[u.h].head, "@@", 3)[1])
}

func (p *parsed) describeLine(lu lineUnit) string {
	u := p.units[lu.u]
	l := p.files[u.f].hunks[u.h].lines[lu.l]
	t := strings.TrimRight(l.text[1:], "\n")
	if strings.TrimSpace(t) == "" {
		t = "(blank)"
	}
	if len(t) > 60 {
		t = t[:60] + "..."
	}
	return fmt.Sprintf("%s: %c %s  [in %s]", p.files[u.f].path, l.kind, t, strings.TrimSpace(strings.SplitN(p.files[u.f].hunks[u.h].head, "@@", 3)[1]))
}
