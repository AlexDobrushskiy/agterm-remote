# agterm-remote

TUI for attaching agterm sessions from other machines (`agtermctl zmx tree/attach`), several at once.

- Hosts: `~/.config/agterm/remotes.conf`, one `destination [label]` per line (the same file agterm 0.36's File ▸ Attach Remote… reads). Each destination needs key-based ssh and an agterm in Live sessions mode with `agtermctl` on its ssh PATH.
- Launch: `ctrl+shift+a` in agterm (overlay; `~/.config/agterm/keymap.conf`), or run `agterm-remote` in any session.
- Install: `go build -o ~/.local/bin/agterm-remote .`

| Key | Does |
|---|---|
| space / a | select the row (a header selects its group) / select all visible |
| / | filter by name, workspace, directory, command |
| enter | attach selected (or the row under the cursor) into this window's current workspace |
| w | selected → one new window, remote workspaces recreated by name |
| W | all visible → one new window per remote window, workspaces recreated |
| tab, 1–9 | switch host · r refresh · q quit |

Sessions already attached on this Mac (`◆ here`) are skipped; the confirm prompt offers `f` to attach them anyway. `◇ open` = attached on some other Mac. The dot is the origin's agent status.
