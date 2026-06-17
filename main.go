package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func homeDir() (string, error) { return os.UserHomeDir() }

func main() {
	cfg := LoadConfig()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--dump", "dump":
			dump(cfg)
			return
		case "rename":
			renameCLI(cfg, os.Args[2:])
			return
		case "-h", "--help", "help":
			usage()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown argument %q\n\n", os.Args[1])
			usage()
			os.Exit(2)
		}
	}

	p := tea.NewProgram(newModel(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`claude-sessions — TUI manager for Claude Code session transcripts

usage:
  claude-sessions                       launch the TUI (Sessions / Trash tabs)
  claude-sessions --dump                print sessions and trash as text
  claude-sessions rename <id> <title>   rename a session (id = prefix ok)
  claude-sessions --help                show this help

The TUI and claude-sessions.sh share one trash dir, so they interoperate.
Renaming appends an ai-title entry to the transcript, so the new name also
shows up in Claude Code's own resume picker (claude --resume).
Config dir: $CLAUDE_CONFIG_DIR (falls back to ~/.claude).

keys (TUI):
  tab        switch Sessions / Trash
  enter      preview transcript        / filter
  r          (Sessions) rename         d  (Sessions) move to trash
  r          (Trash) recover           D  (Trash) delete one permanently
  x          (Trash) empty trash       q  quit
`)
}

func dump(cfg Config) {
	sessions, _ := cfg.ScanSessions()
	fmt.Printf("== sessions (%d) ==\n", len(sessions))
	for _, s := range sessions {
		mark := " "
		if s.Marked {
			mark = "●"
		}
		live := ""
		if s.Live {
			live = " ◉live"
		}
		fmt.Printf("%s %-9s %-7s %s  [%s]%s\n", mark, humanSize(s.Size), relTime(s.ModTime), truncate(s.Title, 50), shortProject(s.Project), live)
	}
	trash, _ := cfg.ScanTrash()
	fmt.Printf("\n== trash (%d) ==\n", len(trash))
	for _, t := range trash {
		fmt.Printf("  %-9s %-7s %s\n     base:   %s\n     origin: %s\n", humanSize(t.Size), relTime(t.ModTime), truncate(t.Title, 50), t.Base, t.Origin)
	}
}

func renameCLI(cfg Config, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: claude-sessions rename <session-id-or-prefix> <new title...>")
		os.Exit(2)
	}
	key := args[0]
	title := strings.Join(args[1:], " ")
	s, ok := cfg.FindByIDPrefix(key)
	if !ok {
		fmt.Fprintf(os.Stderr, "no session matching %q\n", key)
		os.Exit(1)
	}
	if err := cfg.Rename(s, title); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("renamed %s → %q\n", s.ID, cleanOneLine(title))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
