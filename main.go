// agterm-remote browses the agterm sessions on the hosts in remotes.conf and attaches the ones you pick,
// into this window or into new windows that mirror the remote workspaces.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	hostsPath := flag.String("hosts", defaultHostsPath(), "remotes file: one `destination [label...]` per line")
	window := flag.String("window", os.Getenv("AGTERM_WINDOW_ID"), "local window that \"attach here\" targets (default: the active one)")
	socket := flag.String("socket", os.Getenv("AGTERM_SOCKET"), "agterm control socket")
	bin := flag.String("agtermctl", "", "agtermctl binary (default: PATH, then the app bundle)")
	flag.Parse()

	hosts, err := loadHosts(*hostsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agterm-remote: %v\nlist hosts in %s, one per line: destination [label]\n", err, *hostsPath)
		os.Exit(1)
	}
	if *window == "" {
		*window = "active"
	}

	m := newModel(ctl{bin: findCtl(*bin), socket: *socket}, hosts, *window)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agterm-remote:", err)
		os.Exit(1)
	}
}
