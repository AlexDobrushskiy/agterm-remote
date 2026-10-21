package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	styleAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleTab     = lipgloss.NewStyle().Padding(0, 1)
	styleTabOn   = lipgloss.NewStyle().Padding(0, 1).Reverse(true).Bold(true)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleHere    = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	styleConfirm = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

// header line + footer status line + footer key line
const chromeLines = 3

func (m *model) bodyHeight() int { return m.height - chromeLines }

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	var body []string
	switch {
	case m.mode == modeRun:
		body = m.progressView()
	default:
		body = m.listView()
	}
	h := m.bodyHeight()
	if len(body) > h {
		body = body[len(body)-h:]
	}
	for len(body) < h {
		body = append(body, "")
	}
	lines := append([]string{m.tabsView()}, body...)
	lines = append(lines, m.footerView()...)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m *model) tabsView() string {
	parts := []string{styleTitle.Render(" agterm remote ")}
	for i, hs := range m.hosts {
		var count string
		switch {
		case hs.loading:
			count = m.spin.View()
		case hs.err != nil:
			count = "!"
		default:
			count = fmt.Sprint(len(hs.sessions))
		}
		label := fmt.Sprintf("%d %s %s", i+1, hs.host.Label, count)
		if i == m.cur {
			parts = append(parts, styleTabOn.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (m *model) listView() []string {
	hs := m.hosts[m.cur]
	switch {
	case hs.loading && len(hs.sessions) == 0:
		return []string{"", "  " + m.spin.View() + " reading sessions from " + hs.host.Dest + "…"}
	case hs.err != nil:
		return []string{"", "  " + styleErr.Render(hs.host.Dest+": "+hs.err.Error()), "",
			styleDim.Render("  r retry · tab next host")}
	case len(hs.sessions) == 0:
		return []string{"", "  no attachable sessions on " + hs.host.Dest,
			styleDim.Render("  its agterm must run with Live sessions (agtermctl restore mode live, then restart it)")}
	}

	rows := m.rows(hs)
	if len(rows) == 0 {
		return []string{"", styleDim.Render("  nothing matches the filter")}
	}
	nameW := 0
	for _, r := range rows {
		if !r.header {
			nameW = max(nameW, ansi.StringWidth(r.s.Name))
		}
	}
	nameW = min(nameW, max(16, m.width/3))

	end := min(len(rows), hs.offset+m.bodyHeight())
	var out []string
	for i := hs.offset; i < end; i++ {
		out = append(out, m.rowView(hs, rows[i], i == hs.cursor, nameW))
	}
	return out
}

func (m *model) rowView(hs *hostState, r row, cursor bool, nameW int) string {
	pointer := "  "
	if cursor {
		pointer = styleAccent.Render("› ")
	}
	indent := strings.Repeat("  ", r.depth)

	if r.header {
		n := 0
		for _, s := range r.children {
			if hs.selected[s.ID] {
				n++
			}
		}
		box := "[ ]"
		switch {
		case n > 0 && n == len(r.children):
			box = styleAccent.Render("[x]")
		case n > 0:
			box = styleAccent.Render("[-]")
		}
		title := styleTitle.Render(r.title)
		if cursor {
			title = styleAccent.Bold(true).Render(r.title)
		}
		return pointer + indent + box + " " + title + styleDim.Render(fmt.Sprintf(" (%d)", len(r.children)))
	}

	s := r.s
	box := "[ ]"
	if hs.selected[s.ID] {
		box = styleAccent.Render("[x]")
	}
	name := ansi.Truncate(s.Name, nameW, "…")
	name += strings.Repeat(" ", nameW-ansi.StringWidth(name))
	if cursor {
		name = styleAccent.Bold(true).Render(name)
	}

	badge := "      "
	switch {
	case s.AttachedHere:
		badge = styleHere.Render("◆ here")
	case s.Mirrored:
		badge = styleDim.Render("◇ open")
	}

	detail := s.context()
	if detail == "" {
		detail = s.command()
	}
	tail := shortPath(s.Cwd)
	if detail != "" {
		tail += "  " + detail
	}
	return pointer + indent + box + " " + statusDot(s.Status) + " " + name + "  " + badge + "  " + styleDim.Render(tail)
}

func statusDot(status string) string {
	switch status {
	case "active":
		return styleOK.Render("●")
	case "blocked":
		return styleErr.Render("●")
	case "completed":
		return styleAccent.Render("●")
	case "", "idle":
		return styleDim.Render("○")
	default:
		return styleWarn.Render("●")
	}
}

func shortPath(p string) string {
	for _, prefix := range []string{"/home/", "/Users/"} {
		if strings.HasPrefix(p, prefix) {
			rest := strings.TrimPrefix(p, prefix)
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				return "~" + rest[i:]
			}
			return "~"
		}
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (m *model) progressView() []string {
	out := []string{"", "  " + styleTitle.Render(m.planSummary()), ""}
	for _, p := range m.progress {
		var mark string
		switch p.state {
		case stepRunning:
			mark = m.spin.View()
		case stepOK:
			mark = styleOK.Render("✓")
		case stepFailed:
			mark = styleErr.Render("✗")
		}
		line := "  " + mark + " " + p.label
		if p.err != nil {
			line += "  " + styleErr.Render(p.err.Error())
		}
		out = append(out, line)
	}
	return out
}

func (m *model) planSummary() string {
	p := m.plan
	n := plural(p.count(), "session")
	switch p.act {
	case actHere:
		return fmt.Sprintf("Attach %s from %s into this window", n, p.host.Label)
	case actNewWindow:
		return fmt.Sprintf("Attach %s from %s into a new window “%s”", n, p.host.Label, p.host.Label)
	default:
		return fmt.Sprintf("Attach %s from %s into %s", n, p.host.Label, plural(len(p.windows), "new window"))
	}
}

func (m *model) footerView() []string {
	hs := m.hosts[m.cur]
	switch m.mode {
	case modeConfirm:
		p := m.plan
		status := styleConfirm.Render(m.planSummary() + "?")
		if p.count() == 0 {
			status = styleConfirm.Render(fmt.Sprintf("All %s already attached here.", plural(len(p.skipped), "session")))
		} else if len(p.skipped) > 0 {
			status += styleDim.Render(fmt.Sprintf("  (%s already attached here: skipped)", plural(len(p.skipped), "session")))
		}
		keys := "y attach · n cancel"
		if len(p.skipped) > 0 {
			keys = "y attach · f attach the already-attached too · n cancel"
			if p.count() == 0 {
				keys = "f attach them again · n cancel"
			}
		}
		return []string{status, styleDim.Render(keys)}

	case modeRun:
		if m.done == nil {
			return []string{"", styleDim.Render("attaching… ctrl+c abort")}
		}
		status := styleOK.Render(fmt.Sprintf("Attached %d.", m.done.ok))
		if m.done.failed > 0 {
			status += " " + styleErr.Render(fmt.Sprintf("%d failed.", m.done.failed))
		}
		return []string{status, styleDim.Render("b back to the list · any other key closes")}
	}

	var status string
	if m.mode == modeFilter {
		status = m.filter.View()
	} else {
		var parts []string
		if n := len(hs.selected); n > 0 {
			parts = append(parts, styleAccent.Render(fmt.Sprintf("%d selected", n)))
		}
		if q := m.filter.Value(); q != "" {
			parts = append(parts, "filter: "+q)
		}
		if m.note != "" {
			parts = append(parts, styleWarn.Render(m.note))
		}
		status = strings.Join(parts, " · ")
	}
	keys := "space select · a all · / filter · enter attach here · w → new window · W all → new windows · r refresh · tab host · q quit"
	return []string{status, styleDim.Render(keys)}
}
