# claude-sessions

A cross-platform (macOS + Linux) terminal UI for managing Claude Code session
transcripts — the same job the `claude-sessions.sh` purge/recover flow does, but
as a browsable two-pane app. Single static Go binary, no runtime deps.

It shares one trash directory with `claude-sessions.sh`, so the two are fully
interoperable: a session trashed in the TUI can be recovered from the script and
vice-versa.

## What it does

- **Sessions tab** — every transcript across all projects, newest first, shown by
  AI title / first prompt, project path, age and size. `●` marks a session that
  is pending purge-on-exit. Press `d` to move one to the trash, `r` to rename.
- **Rename (`r`)** — sets a session's title the way Claude Code's `/rename` does:
  appends `ai-title` + `custom-title` + `agent-name` entries to the transcript.
  The new name shows in `claude --resume`, this TUI, the statusline, and (via
  `agent-name`) the bottom prompt-bar label after you next open the session.
  - **Caveat (Claude Code bug [#26240]/[#47197]):** the resume picker only scans
    near the *end* of the transcript for a title. If you later resume the session
    and keep working, Claude appends lines after the title entry and the picker
    can fall back to the UUID. Renaming *finished* sessions is reliable; for a
    *live* session use the native `/rename <name>` (which also updates the prompt
    bar via an `agent-name` entry this tool does not write).
  - The bundled `statusline.rb` has a `Title` section that reads the latest
    `ai-title`/`custom-title` from the transcript, so the bottom statusline shows
    the session name regardless of that picker bug.
  - **Live sessions** (a running Claude Code process, marked `◉ live` in the list)
    keep their name in memory and re-emit it on continued use, so an external
    rename of a live session won't update its prompt bar and may be overwritten.
    The app warns when you rename a live session — rename closed ones for a name
    that sticks, or use the in-session `/rename` for a live one. The prompt-bar
    label is driven by the last `agent-name`; the tool now writes it too, so a
    closed session renamed here shows the right label on the next open.
  - Title precedence (matching Claude Code): a manual `custom-title` always wins
    over the auto `ai-title`, which can go stale after a rename.

[#26240]: https://github.com/anthropics/claude-code/issues/26240
[#47197]: https://github.com/anthropics/claude-code/issues/47197
- **Trash tab** — trashed sessions. Recover (`r`), delete one permanently (`D`),
  or empty the whole trash (`x`).
- **Preview** — `enter` renders the conversation (user / assistant turns, tool
  calls summarised), capped so a 20 MB transcript stays snappy.
- `/` filters the active list; `tab` switches tabs; `q` quits.

What it deliberately does **not** do: `mark` / `finalize` (purge-the-current-
session-on-exit). That only makes sense from inside a running session and stays
in the `/purge` slash command + SessionEnd hook.

## Build & install

```sh
make install        # -> $CLAUDE_CONFIG_DIR/bin/claude-sessions  (or ~/.claude/bin)
make test           # filesystem round-trip tests
make cross          # dist/ binaries for linux+darwin, amd64+arm64
```

Config dir resolves from `$CLAUDE_CONFIG_DIR`, falling back to `~/.claude` —
identical to `claude-sessions.sh`. Put `$CLAUDE_CONFIG_DIR/bin` on your `PATH`
(or symlink the binary) and run `claude-sessions`.

For the Mac: `make cross` produces `dist/claude-sessions-darwin-arm64` (Apple
Silicon) and `-darwin-amd64` (Intel); copy the right one to that machine's
`~/.claude/bin/claude-sessions`.

## Headless

`claude-sessions --dump` prints the Sessions and Trash lists as plain text and
exits — handy for scripts or a quick look without entering the UI.

`claude-sessions rename <id-or-prefix> <new title…>` renames a session without
opening the UI (the same append-an-`ai-title` mechanism described above).
