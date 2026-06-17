package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPurgeRecoverEmpty exercises the full filesystem round-trip in a sandbox
// config dir, matching the on-disk contract of claude-sessions.sh.
func TestPurgeRecoverEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		ConfigDir:   dir,
		ProjectsDir: filepath.Join(dir, "projects"),
		TrashDir:    filepath.Join(dir, "session-trash"),
		PendingDir:  filepath.Join(dir, "session-pending"),
	}
	projDir := filepath.Join(cfg.ProjectsDir, "-home-user-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "abc123"
	transcript := filepath.Join(projDir, id+".jsonl")
	content := `{"type":"user","cwd":"/home/user/proj","message":{"content":"hello world"}}` + "\n" +
		`{"type":"ai-title","aiTitle":"My Session Title"}` + "\n" +
		`{"type":"assistant","message":{"content":"hi back"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// scan
	sessions, _ := cfg.ScanSessions()
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].Title != "My Session Title" {
		t.Fatalf("title mismatch: %q", sessions[0].Title)
	}
	if sessions[0].Project != "/home/user/proj" {
		t.Fatalf("project mismatch: %q", sessions[0].Project)
	}

	// purge -> trash
	base, err := cfg.Purge(sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Fatal("original transcript should be gone after purge")
	}
	trash, _ := cfg.ScanTrash()
	if len(trash) != 1 || trash[0].Base != base {
		t.Fatalf("trash scan mismatch: %+v", trash)
	}
	if trash[0].Origin != transcript {
		t.Fatalf("origin mismatch: %q != %q", trash[0].Origin, transcript)
	}
	if trash[0].Title != "My Session Title" {
		t.Fatalf("trash title mismatch: %q", trash[0].Title)
	}

	// recover -> back to origin
	dest, err := cfg.Recover(trash[0])
	if err != nil {
		t.Fatal(err)
	}
	if dest != transcript {
		t.Fatalf("recover dest mismatch: %q", dest)
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatal("transcript should be restored")
	}
	if trash, _ := cfg.ScanTrash(); len(trash) != 0 {
		t.Fatalf("trash should be empty after recover, got %d", len(trash))
	}

	// purge again then empty
	sessions, _ = cfg.ScanSessions()
	if _, err := cfg.Purge(sessions[0]); err != nil {
		t.Fatal(err)
	}
	n, err := cfg.Empty()
	if err != nil || n != 1 {
		t.Fatalf("empty mismatch: n=%d err=%v", n, err)
	}
	if trash, _ := cfg.ScanTrash(); len(trash) != 0 {
		t.Fatal("trash should be empty after Empty()")
	}
}

// TestRename verifies renaming appends an ai-title entry that the tail scan then
// reports as the current title (the same mechanism Claude Code's picker reads).
func TestRename(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		ConfigDir:   dir,
		ProjectsDir: filepath.Join(dir, "projects"),
		TrashDir:    filepath.Join(dir, "session-trash"),
		PendingDir:  filepath.Join(dir, "session-pending"),
	}
	projDir := filepath.Join(cfg.ProjectsDir, "-p")
	os.MkdirAll(projDir, 0o755)
	id := "sess-1"
	transcript := filepath.Join(projDir, id+".jsonl")
	os.WriteFile(transcript, []byte(
		`{"type":"user","cwd":"/p","sessionId":"sess-1","message":{"content":"hi"}}`+"\n"+
			`{"type":"ai-title","aiTitle":"Original Title","sessionId":"sess-1"}`+"\n"), 0o644)

	sessions, _ := cfg.ScanSessions()
	if sessions[0].Title != "Original Title" {
		t.Fatalf("pre-rename title: %q", sessions[0].Title)
	}
	if err := cfg.Rename(sessions[0], "My New Name"); err != nil {
		t.Fatal(err)
	}
	sessions, _ = cfg.ScanSessions()
	if sessions[0].Title != "My New Name" {
		t.Fatalf("post-rename title: %q", sessions[0].Title)
	}
	// rename appends an ai-title then a custom-title (the field /resume reads for
	// manual names) as the very last line, both valid minimal entries.
	data, _ := os.ReadFile(transcript)
	var nonEmpty []string
	for _, l := range splitLines(string(data)) {
		if l != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	// rename appends ai-title, custom-title, then agent-name (the prompt-bar
	// field) as the last three lines — matching what /rename writes.
	got := nonEmpty[len(nonEmpty)-3:]
	want := []string{
		`{"type":"ai-title","aiTitle":"My New Name","sessionId":"sess-1"}`,
		`{"type":"custom-title","customTitle":"My New Name","sessionId":"sess-1"}`,
		`{"type":"agent-name","agentName":"My New Name","sessionId":"sess-1"}`,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d:\n got=%s\nwant=%s", i, got[i], want[i])
		}
	}
	if err := cfg.Rename(sessions[0], "   "); err == nil {
		t.Fatal("expected error on empty title")
	}
}

// TestLiveSessionIDs checks that a sessions/*.json pointing at a live pid (this
// test process) is reported live, while a bogus pid is not.
func TestLiveSessionIDs(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ConfigDir: dir}
	sdir := filepath.Join(dir, "sessions")
	os.MkdirAll(sdir, 0o755)
	os.WriteFile(filepath.Join(sdir, "a.json"),
		[]byte(`{"pid":`+itoa(os.Getpid())+`,"sessionId":"live-one"}`), 0o644)
	os.WriteFile(filepath.Join(sdir, "b.json"),
		[]byte(`{"pid":2147480000,"sessionId":"dead-one"}`), 0o644)

	live := cfg.LiveSessionIDs()
	if !live["live-one"] {
		t.Fatal("expected live-one to be detected as live")
	}
	if live["dead-one"] {
		t.Fatal("dead-one should not be live")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

// TestRecoverStubsExistingFile verifies a pre-existing file at the destination
// is preserved (moved aside) rather than overwritten.
func TestRecoverStubsExistingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		ConfigDir:   dir,
		ProjectsDir: filepath.Join(dir, "projects"),
		TrashDir:    filepath.Join(dir, "session-trash"),
		PendingDir:  filepath.Join(dir, "session-pending"),
	}
	projDir := filepath.Join(cfg.ProjectsDir, "-p")
	os.MkdirAll(projDir, 0o755)
	transcript := filepath.Join(projDir, "x.jsonl")
	os.WriteFile(transcript, []byte(`{"type":"user","cwd":"/p","message":{"content":"orig"}}`+"\n"), 0o644)

	sessions, _ := cfg.ScanSessions()
	cfg.Purge(sessions[0])

	// a new session now occupies the original path
	os.WriteFile(transcript, []byte(`{"type":"user","cwd":"/p","message":{"content":"new"}}`+"\n"), 0o644)

	trash, _ := cfg.ScanTrash()
	if _, err := cfg.Recover(trash[0]); err != nil {
		t.Fatal(err)
	}
	stubs, _ := filepath.Glob(transcript + ".stub-*")
	if len(stubs) != 1 {
		t.Fatalf("expected 1 stub of the pre-existing file, got %d", len(stubs))
	}
}
