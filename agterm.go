package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	listTimeout   = 90 * time.Second
	actionTimeout = 60 * time.Second
	localTimeout  = 10 * time.Second
)

// ctl runs agtermctl against the local app.
type ctl struct {
	bin    string
	socket string
}

func findCtl(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if p, err := exec.LookPath("agtermctl"); err == nil {
		return p
	}
	return "/Applications/agterm.app/Contents/MacOS/agtermctl"
}

type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func (c ctl) call(timeout time.Duration, args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args = append(args, "--json")
	if c.socket != "" {
		args = append(args, "--socket", c.socket)
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	var r response
	if err := json.Unmarshal(out, &r); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("agtermctl %s timed out", args[0])
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("agtermctl %s: unreadable reply", args[0])
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Result, nil
}

type Pane struct {
	Pane       string   `json:"pane"`
	Foreground []string `json:"foreground"`
}

// Session is one attachable session from `zmx tree HOST`, plus what we learn from both sides.
type Session struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Cwd           string          `json:"cwd"`
	Context       json.RawMessage `json:"context"`
	WindowID      string          `json:"windowID"`
	WindowName    string          `json:"windowName"`
	WorkspaceID   string          `json:"workspaceID"`
	WorkspaceName string          `json:"workspaceName"`
	SplitAxis     string          `json:"splitAxis"`
	Panes         []Pane          `json:"panes"`

	Status       string `json:"-"` // the origin's agent status: active, blocked, completed, ...
	Mirrored     bool   `json:"-"` // some Mac has it attached
	AttachedHere bool   `json:"-"` // this Mac has a remote row for it
}

func (s *Session) label() string { return s.WorkspaceName + "/" + s.Name }

type treeSession struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	RemoteHost string `json:"remoteHost"`
	Presenters *struct {
		Mirrors   int  `json:"mirrors"`
		Presenter bool `json:"presenter"`
	} `json:"presenters"`
}

type treeResult struct {
	Tree struct {
		Workspaces []struct {
			ID       string        `json:"id"`
			Sessions []treeSession `json:"sessions"`
		} `json:"workspaces"`
	} `json:"tree"`
}

func (c ctl) loadHost(h Host) ([]*Session, error) {
	raw, err := c.call(listTimeout, "zmx", "tree", h.Dest)
	if err != nil {
		return nil, err
	}
	var res struct {
		Remote struct {
			Sessions []*Session `json:"sessions"`
		} `json:"remote"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("zmx tree %s: %w", h.Dest, err)
	}
	sessions := res.Remote.Sessions

	// Status and attachment are best-effort: a failure here leaves the list usable.
	var origin map[string]treeSession
	var here map[string]bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); origin = originSessions(h.Dest, sessions) }()
	go func() { defer wg.Done(); here = c.attachedNames(h.Dest) }()
	wg.Wait()

	for _, s := range sessions {
		if o, ok := origin[s.ID]; ok {
			s.Status = o.Status
			s.Mirrored = o.Presenters != nil && (o.Presenters.Presenter || o.Presenters.Mirrors > 0)
		}
		s.AttachedHere = here[s.Name]
	}
	return sessions, nil
}

var uuidRe = regexp.MustCompile(`^[0-9A-Fa-f-]{36}$`)

// originSessions reads the origin's own tree for each window `zmx tree` named, in one ssh call.
func originSessions(dest string, sessions []*Session) map[string]treeSession {
	var script []string
	seen := map[string]bool{}
	for _, s := range sessions {
		if seen[s.WindowID] || !uuidRe.MatchString(s.WindowID) {
			continue
		}
		seen[s.WindowID] = true
		script = append(script, "agtermctl tree --json --window "+s.WindowID)
	}
	if len(script) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		dest, strings.Join(script, "; ")).Output()

	found := map[string]treeSession{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r struct {
			OK     bool       `json:"ok"`
			Result treeResult `json:"result"`
		}
		if dec.Decode(&r) != nil {
			break
		}
		for _, ws := range r.Result.Tree.Workspaces {
			for _, s := range ws.Sessions {
				found[s.ID] = s
			}
		}
	}
	return found
}

// attachedNames is the set of session names this Mac already shows as remote rows from dest.
func (c ctl) attachedNames(dest string) map[string]bool {
	names := map[string]bool{}
	raw, err := c.call(localTimeout, "window", "list")
	if err != nil {
		return names
	}
	var wl struct {
		Windows []struct {
			ID   string `json:"id"`
			Open bool   `json:"open"`
		} `json:"windows"`
	}
	if json.Unmarshal(raw, &wl) != nil {
		return names
	}
	for _, w := range wl.Windows {
		if !w.Open {
			continue
		}
		tree, err := c.tree(w.ID)
		if err != nil {
			continue
		}
		for _, ws := range tree.Tree.Workspaces {
			for _, s := range ws.Sessions {
				if s.RemoteHost == dest {
					names[s.Name] = true
				}
			}
		}
	}
	return names
}

func (c ctl) tree(window string) (treeResult, error) {
	var t treeResult
	raw, err := c.call(localTimeout, "tree", "--window", window)
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(raw, &t)
	return t, err
}

func (c ctl) resultID(timeout time.Duration, args ...string) (string, error) {
	raw, err := c.call(timeout, args...)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

func (c ctl) newWindow(name string) (string, error) {
	return c.resultID(localTimeout, "window", "new", name)
}

func (c ctl) workspaceIDs(window string) []string {
	t, err := c.tree(window)
	if err != nil {
		return nil
	}
	var ids []string
	for _, ws := range t.Tree.Workspaces {
		ids = append(ids, ws.ID)
	}
	return ids
}

func (c ctl) newWorkspace(name, window string) error {
	_, err := c.call(localTimeout, "workspace", "new", name, "--window", window)
	return err
}

func (c ctl) deleteWorkspace(id, window string) error {
	_, err := c.call(localTimeout, "workspace", "delete", "--target", id, "--window", window)
	return err
}

func (c ctl) attach(dest, session, window string) error {
	_, err := c.call(actionTimeout, "zmx", "attach", dest, session, "--window", window)
	return err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "error: ")
}
