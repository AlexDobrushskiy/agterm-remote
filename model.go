package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type mode int

const (
	modeBrowse mode = iota
	modeFilter
	modeConfirm
	modeRun
)

type hostState struct {
	host     Host
	loading  bool
	err      error
	sessions []*Session
	selected map[string]bool
	cursor   int
	offset   int
}

// row is one line of the session list: a group header or a session.
type row struct {
	header   bool
	depth    int
	title    string
	children []*Session // header only: its visible sessions
	s        *Session
}

type model struct {
	ctl    ctl
	target string // window for "attach here"
	hosts  []*hostState
	cur    int

	width, height int
	mode          mode
	filter        textinput.Model
	spin          spinner.Model
	note          string

	plan     *plan
	progress []progressMsg
	progCh   chan tea.Msg
	done     *runDoneMsg
}

type loadedMsg struct {
	idx      int
	sessions []*Session
	err      error
}

type quitMsg struct{}

func newModel(c ctl, hosts []Host, target string) *model {
	m := &model{ctl: c, target: target}
	for _, h := range hosts {
		m.hosts = append(m.hosts, &hostState{host: h, loading: true, selected: map[string]bool{}})
	}
	m.filter = textinput.New()
	m.filter.Prompt = "/"
	m.filter.Placeholder = "filter by name, workspace, directory"
	m.spin = spinner.New()
	m.spin.Spinner = spinner.MiniDot
	m.spin.Style = styleAccent
	return m
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Tick}
	for i := range m.hosts {
		cmds = append(cmds, m.load(i))
	}
	return tea.Batch(cmds...)
}

func (m *model) load(i int) tea.Cmd {
	m.hosts[i].loading = true
	c, h := m.ctl, m.hosts[i].host
	return func() tea.Msg {
		s, err := c.loadHost(h)
		return loadedMsg{idx: i, sessions: s, err: err}
	}
}

func waitFor(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case loadedMsg:
		hs := m.hosts[msg.idx]
		hs.loading, hs.err, hs.sessions = false, msg.err, msg.sessions
		live := map[string]bool{}
		for _, s := range hs.sessions {
			live[s.ID] = true
		}
		for id := range hs.selected {
			if !live[id] {
				delete(hs.selected, id)
			}
		}
		m.scroll()
		return m, nil

	case progressMsg:
		if msg.idx >= len(m.progress) {
			m.progress = append(m.progress, msg)
		} else {
			p := &m.progress[msg.idx]
			p.state, p.err = msg.state, msg.err
		}
		return m, waitFor(m.progCh)

	case runDoneMsg:
		m.done = &msg
		if msg.failed == 0 {
			return m, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg { return quitMsg{} })
		}
		return m, nil

	case quitMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		case modeRun:
			if m.done != nil {
				if msg.String() == "b" {
					m.mode, m.plan, m.progress, m.done = modeBrowse, nil, nil, nil
					return m, m.load(m.cur)
				}
				return m, tea.Quit
			}
			return m, nil
		}
		return m.updateBrowse(msg)
	}
	return m, nil
}

func (m *model) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	hs := m.hosts[m.cur]
	m.note = ""
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.scroll()
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.bodyHeight())
	case "pgdown", "ctrl+d":
		m.move(m.bodyHeight())
	case "home", "g":
		m.move(-1 << 20)
	case "end", "G":
		m.move(1 << 20)
	case "tab", "right", "l":
		m.switchHost(m.cur + 1)
	case "shift+tab", "left", "h":
		m.switchHost(m.cur - 1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.switchHost(int(msg.String()[0] - '1'))
	case " ", "x":
		rows := m.rows(hs)
		if len(rows) == 0 {
			break
		}
		r := rows[hs.cursor]
		if r.header {
			toggle(hs, r.children)
		} else {
			setSelected(hs, r.s, !hs.selected[r.s.ID])
			m.move(1)
		}
	case "a":
		toggle(hs, m.visible(hs))
	case "/":
		m.mode = modeFilter
		return m, m.filter.Focus()
	case "r":
		return m, m.load(m.cur)
	case "enter":
		return m.start(actHere)
	case "w":
		return m.start(actNewWindow)
	case "W":
		return m.start(actAllNewWindows)
	}
	return m, nil
}

func (m *model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filter.SetValue("")
		fallthrough
	case "enter", "down", "up":
		m.mode = modeBrowse
		m.filter.Blur()
		m.scroll()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.hosts[m.cur].cursor = 0
	m.scroll()
	return m, cmd
}

func (m *model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if m.plan.count() > 0 {
			return m.run(m.plan)
		}
	case "f":
		if len(m.plan.skipped) > 0 {
			p := m.plan
			return m.run(buildPlan(p.act, p.host, p.targets, m.target, true))
		}
	case "n", "esc", "q":
		m.mode, m.plan = modeBrowse, nil
	}
	return m, nil
}

func (m *model) start(act action) (tea.Model, tea.Cmd) {
	hs := m.hosts[m.cur]
	if hs.loading || hs.err != nil || len(hs.sessions) == 0 {
		m.note = "no sessions to attach on " + hs.host.Label
		return m, nil
	}
	var targets []*Session
	if act == actAllNewWindows {
		targets = m.visible(hs)
	} else {
		for _, s := range hs.sessions {
			if hs.selected[s.ID] {
				targets = append(targets, s)
			}
		}
		if len(targets) == 0 {
			rows := m.rows(hs)
			if len(rows) == 0 {
				return m, nil
			}
			if r := rows[hs.cursor]; r.header {
				targets = r.children
			} else {
				targets = []*Session{r.s}
			}
		}
	}
	p := buildPlan(act, hs.host, targets, m.target, false)
	if act == actHere && p.count() == 1 && len(p.skipped) == 0 {
		return m.run(p)
	}
	m.plan, m.mode = p, modeConfirm
	return m, nil
}

func (m *model) run(p *plan) (tea.Model, tea.Cmd) {
	m.plan, m.mode, m.progress, m.done = p, modeRun, nil, nil
	m.progCh = make(chan tea.Msg, 16)
	go execute(m.ctl, p, m.progCh)
	return m, waitFor(m.progCh)
}

func toggle(hs *hostState, sessions []*Session) {
	all := len(sessions) > 0
	for _, s := range sessions {
		all = all && hs.selected[s.ID]
	}
	for _, s := range sessions {
		setSelected(hs, s, !all)
	}
}

// setSelected keeps only selected ids in the map, so its length is the selection count.
func setSelected(hs *hostState, s *Session, on bool) {
	if on {
		hs.selected[s.ID] = true
	} else {
		delete(hs.selected, s.ID)
	}
}

func (m *model) switchHost(i int) {
	n := len(m.hosts)
	m.cur = ((i % n) + n) % n
	m.scroll()
}

func (m *model) move(d int) {
	hs := m.hosts[m.cur]
	hs.cursor += d
	m.scroll()
}

// scroll clamps the cursor to the rows and keeps it on screen.
func (m *model) scroll() {
	hs := m.hosts[m.cur]
	n := len(m.rows(hs))
	hs.cursor = max(0, min(hs.cursor, n-1))
	h := max(1, m.bodyHeight())
	if hs.cursor < hs.offset {
		hs.offset = hs.cursor
	}
	if hs.cursor >= hs.offset+h {
		hs.offset = hs.cursor - h + 1
	}
	hs.offset = max(0, min(hs.offset, n-h))
}

func (m *model) visible(hs *hostState) []*Session {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		return hs.sessions
	}
	var out []*Session
	for _, s := range hs.sessions {
		hay := strings.ToLower(strings.Join([]string{s.Name, s.WorkspaceName, s.WindowName, s.Cwd, s.context(), s.command()}, " "))
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

// rows groups the visible sessions: remote window (only when there are several) ▸ workspace ▸ session.
func (m *model) rows(hs *hostState) []row {
	vis := m.visible(hs)
	multiWindow := len(groupBy(hs.sessions, func(s *Session) string { return s.WindowID })) > 1
	var rows []row
	for _, win := range groupBy(vis, func(s *Session) string { return s.WindowID }) {
		depth := 0
		if multiWindow {
			rows = append(rows, row{header: true, title: win[0].WindowName, children: win})
			depth = 1
		}
		for _, ws := range groupBy(win, func(s *Session) string { return s.WorkspaceID }) {
			rows = append(rows, row{header: true, depth: depth, title: ws[0].WorkspaceName, children: ws})
			for _, s := range ws {
				rows = append(rows, row{depth: depth + 1, s: s})
			}
		}
	}
	return rows
}

func (s *Session) context() string {
	if len(s.Context) == 0 || string(s.Context) == "null" {
		return ""
	}
	var str string
	if json.Unmarshal(s.Context, &str) == nil {
		return str
	}
	return string(s.Context)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true}

// command is the left pane's foreground with the wrapping login shell dropped.
func (s *Session) command() string {
	if len(s.Panes) == 0 {
		return ""
	}
	argv := s.Panes[0].Foreground
	i := 0
	for i < len(argv)-1 && (shells[filepath.Base(argv[i])] || strings.HasPrefix(argv[i], "-")) {
		i++
	}
	if i >= len(argv) {
		return ""
	}
	parts := append([]string{filepath.Base(argv[i])}, argv[i+1:]...)
	return strings.Join(parts, " ")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
