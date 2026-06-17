package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Config mirrors the on-disk layout used by claude-sessions.sh so the two tools
// share one trash and remain fully interoperable.
type Config struct {
	ConfigDir   string
	ProjectsDir string
	TrashDir    string
	PendingDir  string
}

func LoadConfig() Config {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return Config{
		ConfigDir:   dir,
		ProjectsDir: filepath.Join(dir, "projects"),
		TrashDir:    filepath.Join(dir, "session-trash"),
		PendingDir:  filepath.Join(dir, "session-pending"),
	}
}

// Session is an active transcript under projects/<encoded>/<id>.jsonl.
type Session struct {
	Path    string
	ID      string
	Project string
	Title   string
	ModTime time.Time
	Size    int64
	Marked  bool // a pending mark exists (will be purged on session exit)
	Live    bool // a Claude Code process is currently running this session
}

// TrashItem is a (<base>.jsonl + <base>.origin) pair in the session trash.
type TrashItem struct {
	Base       string // ts__id
	JSONLPath  string
	OriginPath string
	Origin     string // destination path to restore to
	Title      string
	Project    string
	ModTime    time.Time
	Size       int64
}

const (
	scanMaxBytes = 512 * 1024 // only read the head of each transcript for metadata
	scanMaxLines = 400
)

// readHead returns up to maxBytes from the start of a file.
func readHead(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

// readTail returns up to maxBytes from the end of a file (a partial leading line
// is dropped by the caller). Used to find the *latest* ai-title, which Claude
// Code re-appends as the session evolves — the last one wins.
func readTail(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	off := int64(0)
	if size > int64(maxBytes) {
		off = size - int64(maxBytes)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		// drop the partial first line
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return buf, nil
}

// scanTitles returns the last custom-title and last ai-title found in a buffer.
// custom-title is the manual name (from /rename or our Rename); ai-title is the
// auto-generated one, which Claude may keep re-emitting with a STALE value after
// a manual rename — so the two must be tracked separately, not "last wins".
func scanTitles(data []byte) (custom, ai string) {
	for _, ln := range bytes.Split(data, []byte{'\n'}) {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		var m lineMeta
		if json.Unmarshal(ln, &m) != nil {
			continue
		}
		if m.CustomTitle != "" {
			custom = m.CustomTitle
		}
		if m.AiTitle != "" {
			ai = m.AiTitle
		}
	}
	return
}

type lineMeta struct {
	Type         string          `json:"type"`
	Cwd          string          `json:"cwd"`
	AiTitle      string          `json:"aiTitle"`
	CustomTitle  string          `json:"customTitle"`
	Slug         string          `json:"slug"`
	GitBranch    string          `json:"gitBranch"`
	MessageCount int             `json:"messageCount"`
	Message      json.RawMessage `json:"message"`
}

// scanMeta extracts a friendly project path and title from a transcript. cwd and
// the first user prompt come from the head; the title is resolved with the same
// precedence Claude Code's resume picker uses — a manual custom-title wins over
// the auto ai-title, and the most recent (tail) value wins over the head.
func scanMeta(path string) (project, title string) {
	head, err := readHead(path, scanMaxBytes)
	if err != nil {
		return "", ""
	}
	var firstUser, headCustom, headAi string
	lines := bytes.Split(head, []byte{'\n'})
	for i, ln := range lines {
		if i >= scanMaxLines {
			break
		}
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 {
			continue
		}
		var m lineMeta
		if json.Unmarshal(ln, &m) != nil {
			continue
		}
		if project == "" && m.Cwd != "" {
			project = m.Cwd
		}
		if m.CustomTitle != "" {
			headCustom = m.CustomTitle
		}
		if m.AiTitle != "" {
			headAi = m.AiTitle
		}
		if firstUser == "" && m.Type == "user" && len(m.Message) > 0 {
			firstUser = firstText(m.Message)
		}
	}

	tailCustom, tailAi := "", ""
	if tail, err := readTail(path, 64*1024); err == nil {
		tailCustom, tailAi = scanTitles(tail)
	}
	custom := firstNonEmpty(tailCustom, headCustom)
	ai := firstNonEmpty(tailAi, headAi)

	title = firstNonEmpty(custom, ai, firstUser) // custom-title beats stale ai-title
	title = cleanOneLine(title)
	return project, title
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstText pulls the first human-readable text out of a message field whose
// content is either a string or a list of content blocks.
func firstText(raw json.RawMessage) string {
	var asString struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &asString) == nil && asString.Content != "" {
		return asString.Content
	}
	var asBlocks struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &asBlocks) == nil {
		for _, b := range asBlocks.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
	}
	return ""
}

func cleanOneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	// strip noisy harness wrappers so the list stays readable
	for _, p := range []string{"<local-command-caveat>", "<command-message>", "<command-name>", "<system-reminder>"} {
		if strings.HasPrefix(s, p) {
			s = "(command) " + strings.TrimSpace(s[len(p):])
			break
		}
	}
	return s
}

// ScanSessions walks every project's transcripts, newest first.
func (c Config) ScanSessions() ([]Session, error) {
	matches, _ := filepath.Glob(filepath.Join(c.ProjectsDir, "*", "*.jsonl"))
	live := c.LiveSessionIDs()
	out := make([]Session, 0, len(matches))
	for _, p := range matches {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		project, title := scanMeta(p)
		if project == "" {
			project = filepath.Base(filepath.Dir(p))
		}
		_, markErr := os.Stat(filepath.Join(c.PendingDir, id))
		out = append(out, Session{
			Path:    p,
			ID:      id,
			Project: project,
			Title:   title,
			ModTime: st.ModTime(),
			Size:    st.Size(),
			Marked:  markErr == nil,
			Live:    live[id],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// LiveSessionIDs returns the set of session ids a running Claude Code process is
// currently attached to, read from $CONFIG/sessions/*.json (pid + sessionId) and
// confirmed against the live process table. Renaming a live session from outside
// is unreliable: the running session keeps its in-memory name (the prompt-bar
// `agent-name`) and re-emits it on continued use, overwriting an external rename.
func (c Config) LiveSessionIDs() map[string]bool {
	out := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(c.ConfigDir, "sessions", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			Pid       int    `json:"pid"`
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if s.SessionID != "" && pidAlive(s.Pid) {
			out[s.SessionID] = true
		}
	}
	return out
}

// pidAlive reports whether a process exists (works on Linux and macOS).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM) // EPERM: exists, not ours
}

// ScanTrash lists trashed sessions, newest first.
func (c Config) ScanTrash() ([]TrashItem, error) {
	matches, _ := filepath.Glob(filepath.Join(c.TrashDir, "*.origin"))
	out := make([]TrashItem, 0, len(matches))
	for _, o := range matches {
		base := strings.TrimSuffix(filepath.Base(o), ".origin")
		jsonl := filepath.Join(c.TrashDir, base+".jsonl")
		originBytes, _ := os.ReadFile(o)
		item := TrashItem{
			Base:       base,
			JSONLPath:  jsonl,
			OriginPath: o,
			Origin:     strings.TrimSpace(string(originBytes)),
		}
		if st, err := os.Stat(jsonl); err == nil {
			item.ModTime = st.ModTime()
			item.Size = st.Size()
			item.Project, item.Title = scanMeta(jsonl)
		}
		if item.Project == "" {
			item.Project = filepath.Dir(item.Origin)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Base > out[j].Base }) // base starts with a sortable timestamp
	return out, nil
}

// moveFile renames, falling back to copy+remove across filesystems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// Purge moves an active session into the trash (same naming as move_to_trash in
// claude-sessions.sh: <ts>__<id>.jsonl + a .origin file holding the original path).
func (c Config) Purge(s Session) (string, error) {
	if err := os.MkdirAll(c.TrashDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405")
	base := ts + "__" + s.ID
	dest := filepath.Join(c.TrashDir, base+".jsonl")
	if err := moveFile(s.Path, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(c.TrashDir, base+".origin"), []byte(s.Path+"\n"), 0o644); err != nil {
		return base, err
	}
	return base, nil
}

// Recover restores a trashed session to its origin path (stashing any existing
// file at the destination, matching the bash recover behaviour).
func (c Config) Recover(t TrashItem) (string, error) {
	dest := strings.TrimSpace(t.Origin)
	if dest == "" {
		return "", fmt.Errorf("no origin recorded for %s", t.Base)
	}
	if _, err := os.Stat(t.JSONLPath); err != nil {
		return "", fmt.Errorf("trashed transcript missing: %s", t.JSONLPath)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(dest); err == nil {
		stub := fmt.Sprintf("%s.stub-%d", dest, time.Now().Unix())
		_ = os.Rename(dest, stub)
	}
	if err := moveFile(t.JSONLPath, dest); err != nil {
		return "", err
	}
	_ = os.Remove(t.OriginPath)
	return dest, nil
}

// Rename sets a session's title the way Claude Code's own `/rename` does: by
// appending the same trio of entries to the transcript —
//   ai-title    -> the resume picker's primary title + this TUI + the statusline
//   custom-title-> the manual name the resume picker prefers over a stale ai-title
//   agent-name  -> the name shown in the bottom prompt bar when the session opens
// Writing agent-name is what makes the prompt-bar label match after you resume;
// without it the bar keeps whatever the last agent-name was (e.g. an old /rename).
//
// Caveats: (1) the resume picker only scans near the END of the transcript
// (Claude Code bug #26240/#47197), so if you later resume and keep working, lines
// appended after these entries can bury the title and revert the picker to the
// UUID. (2) Renaming a *live* session from outside can't update its in-memory
// prompt bar and may be overwritten — rename closed sessions, or use the
// in-session `/rename` for a live one (see Session.Live).
func (c Config) Rename(s Session, title string) error {
	title = cleanOneLine(title)
	if title == "" {
		return fmt.Errorf("empty title")
	}
	ai := struct {
		Type      string `json:"type"`
		AiTitle   string `json:"aiTitle"`
		SessionID string `json:"sessionId"`
	}{"ai-title", title, s.ID}
	custom := struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		SessionID   string `json:"sessionId"`
	}{"custom-title", title, s.ID}
	agent := struct {
		Type      string `json:"type"`
		AgentName string `json:"agentName"`
		SessionID string `json:"sessionId"`
	}{"agent-name", title, s.ID}

	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range []any{ai, custom, agent} { // agent-name written last
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// FindByIDPrefix locates a session whose id starts with (or contains) the given
// key — used by the headless `rename` subcommand.
func (c Config) FindByIDPrefix(key string) (Session, bool) {
	sessions, _ := c.ScanSessions()
	for _, s := range sessions { // exact / prefix first
		if s.ID == key || strings.HasPrefix(s.ID, key) {
			return s, true
		}
	}
	for _, s := range sessions {
		if strings.Contains(s.ID, key) {
			return s, true
		}
	}
	return Session{}, false
}

// DeleteOne permanently removes a single trashed session.
func (c Config) DeleteOne(t TrashItem) error {
	_ = os.Remove(t.JSONLPath)
	return os.Remove(t.OriginPath)
}

// Empty permanently deletes everything in the trash, returning the count removed.
func (c Config) Empty() (int, error) {
	origins, _ := filepath.Glob(filepath.Join(c.TrashDir, "*.origin"))
	for _, o := range origins {
		base := strings.TrimSuffix(filepath.Base(o), ".origin")
		_ = os.Remove(filepath.Join(c.TrashDir, base+".jsonl"))
		_ = os.Remove(o)
	}
	return len(origins), nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
