package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Host is one line of agterm's remotes.conf: `destination [label...]`.
type Host struct {
	Dest  string
	Label string
}

// defaultHostsPath is the remotes.conf agterm's own File ▸ Attach Remote… reads, so both share one list.
func defaultHostsPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agterm", "remotes.conf")
}

func loadHosts(path string) ([]Host, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hosts []Host
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		h := Host{Dest: fields[0], Label: fields[0]}
		if len(fields) > 1 {
			h.Label = strings.Join(fields[1:], " ")
		}
		hosts = append(hosts, h)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%s lists no hosts", path)
	}
	return hosts, nil
}
