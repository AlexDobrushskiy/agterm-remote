package main

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
)

type action int

const (
	actHere          action = iota // into the target window's current workspace
	actNewWindow                   // into one new window, workspaces mirrored
	actAllNewWindows               // one new window per remote window, workspaces mirrored
)

type planWorkspace struct {
	name     string // "" = the window's current workspace
	sessions []*Session
}

type planWindow struct {
	name       string
	existing   string // window id; "" = create a window named name
	workspaces []planWorkspace
}

type plan struct {
	act     action
	host    Host
	windows []planWindow
	skipped []*Session // already attached here
	targets []*Session // what the plan was built from, for rebuilding with duplicates
}

func (p *plan) count() int {
	n := 0
	for _, w := range p.windows {
		for _, ws := range w.workspaces {
			n += len(ws.sessions)
		}
	}
	return n
}

func buildPlan(act action, h Host, targets []*Session, targetWindow string, includeAttached bool) *plan {
	p := &plan{act: act, host: h, targets: targets}
	var todo []*Session
	for _, s := range targets {
		if s.AttachedHere && !includeAttached {
			p.skipped = append(p.skipped, s)
			continue
		}
		todo = append(todo, s)
	}
	if len(todo) == 0 {
		return p
	}

	switch act {
	case actHere:
		p.windows = []planWindow{{existing: targetWindow, workspaces: []planWorkspace{{sessions: todo}}}}
	case actNewWindow:
		p.windows = []planWindow{{name: h.Label, workspaces: byWorkspace(todo)}}
	case actAllNewWindows:
		groups := groupBy(todo, func(s *Session) string { return s.WindowID })
		for _, g := range groups {
			name := h.Label
			if len(groups) > 1 {
				name += " · " + g[0].WindowName
			}
			p.windows = append(p.windows, planWindow{name: name, workspaces: byWorkspace(g)})
		}
	}
	return p
}

func byWorkspace(sessions []*Session) []planWorkspace {
	var out []planWorkspace
	for _, g := range groupBy(sessions, func(s *Session) string { return s.WorkspaceID }) {
		out = append(out, planWorkspace{name: g[0].WorkspaceName, sessions: g})
	}
	return out
}

// groupBy keeps first-appearance order of both groups and members.
func groupBy(sessions []*Session, key func(*Session) string) [][]*Session {
	idx := map[string]int{}
	var groups [][]*Session
	for _, s := range sessions {
		k := key(s)
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			idx[k] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], s)
	}
	return groups
}

type stepState int

const (
	stepRunning stepState = iota
	stepOK
	stepFailed
)

type progressMsg struct {
	idx   int
	label string // set on the first message for a step
	state stepState
	err   error
}

type runDoneMsg struct{ ok, failed int }

var errWindowNotCreated = errors.New("window was not created")
var errWorkspaceNotCreated = errors.New("workspace was not created")

// execute runs the plan in order, reporting every step on ch, and closes ch when done.
func execute(c ctl, p *plan, ch chan<- tea.Msg) {
	defer close(ch)
	n, ok, failed := 0, 0, 0
	start := func(label string) int {
		ch <- progressMsg{idx: n, label: label, state: stepRunning}
		n++
		return n - 1
	}
	finish := func(i int, err error) {
		st := stepOK
		if err != nil {
			st = stepFailed
		}
		ch <- progressMsg{idx: i, state: st, err: err}
	}
	failAll := func(sessions []*Session, err error) {
		for _, s := range sessions {
			finish(start(s.label()), err)
			failed++
		}
	}

	for _, w := range p.windows {
		win := w.existing
		var placeholders []string
		if win == "" {
			i := start("new window “" + w.name + "”")
			id, err := c.newWindow(w.name)
			finish(i, err)
			if err != nil {
				for _, ws := range w.workspaces {
					failAll(ws.sessions, errWindowNotCreated)
				}
				continue
			}
			win = id
			placeholders = c.workspaceIDs(win)
		}

		created := 0
		for _, ws := range w.workspaces {
			if ws.name != "" {
				i := start("workspace “" + ws.name + "”")
				err := c.newWorkspace(ws.name, win)
				finish(i, err)
				if err != nil {
					failAll(ws.sessions, errWorkspaceNotCreated)
					continue
				}
				created++
			}
			for _, s := range ws.sessions {
				i := start(s.label())
				err := c.attach(p.host.Dest, s.ID, win)
				finish(i, err)
				if err != nil {
					failed++
				} else {
					ok++
				}
			}
		}
		// A new window comes with a default workspace and shell; drop it once ours exist.
		if created > 0 {
			for _, id := range placeholders {
				_ = c.deleteWorkspace(id, win)
			}
		}
	}
	ch <- runDoneMsg{ok: ok, failed: failed}
}
