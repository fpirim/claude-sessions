package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- list items -----------------------------------------------------------

type sessionItem struct{ s Session }

func (i sessionItem) Title() string {
	mark := ""
	if i.s.Marked {
		mark = "● " // marked to purge on exit
	}
	t := i.s.Title
	if t == "" {
		t = i.s.ID
	}
	return mark + t
}
func (i sessionItem) Description() string {
	live := ""
	if i.s.Live {
		live = " · ◉ live"
	}
	return fmt.Sprintf("%s · %s · %s%s", shortProject(i.s.Project), relTime(i.s.ModTime), humanSize(i.s.Size), live)
}
func (i sessionItem) FilterValue() string { return i.s.Title + " " + i.s.Project + " " + i.s.ID }

type trashItem struct{ t TrashItem }

func (i trashItem) Title() string {
	t := i.t.Title
	if t == "" {
		t = i.t.Base
	}
	return t
}
func (i trashItem) Description() string {
	return fmt.Sprintf("%s · %s · %s", shortProject(i.t.Project), relTime(i.t.ModTime), humanSize(i.t.Size))
}
func (i trashItem) FilterValue() string { return i.t.Title + " " + i.t.Origin + " " + i.t.Base }

// ---- model ----------------------------------------------------------------

type uiMode int

const (
	modeList uiMode = iota
	modePreview
	modeConfirm
	modeInput
)

type confirmState struct {
	prompt string
	action func(m *model) (string, error)
}

type model struct {
	cfg      Config
	tab      int // 0 = sessions, 1 = trash
	sessions list.Model
	trash    list.Model
	viewport viewport.Model
	input    textinput.Model
	rename   Session // session being renamed in modeInput
	mode     uiMode
	confirm  confirmState
	status   string
	width    int
	height   int
}

var (
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("63")).Padding(0, 2)
	tabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 2)
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	confirmBox  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("160")).Padding(0, 1)
	previewHdr  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
)

func newModel(cfg Config) model {
	sd := list.NewDefaultDelegate()
	td := list.NewDefaultDelegate()
	sl := list.New(nil, sd, 0, 0)
	sl.Title = "Sessions"
	sl.SetShowHelp(false)
	sl.SetStatusBarItemName("session", "sessions")
	tl := list.New(nil, td, 0, 0)
	tl.Title = "Trash"
	tl.SetShowHelp(false)
	tl.SetStatusBarItemName("item", "items")

	ti := textinput.New()
	ti.Prompt = "rename: "
	ti.CharLimit = 120

	m := model{cfg: cfg, sessions: sl, trash: tl, viewport: viewport.New(0, 0), input: ti}
	m.reload()
	return m
}

func (m *model) reload() {
	sessions, _ := m.cfg.ScanSessions()
	sitems := make([]list.Item, len(sessions))
	for i, s := range sessions {
		sitems[i] = sessionItem{s}
	}
	m.sessions.SetItems(sitems)

	trash, _ := m.cfg.ScanTrash()
	titems := make([]list.Item, len(trash))
	for i, t := range trash {
		titems[i] = trashItem{t}
	}
	m.trash.SetItems(titems)
}

func (m *model) activeList() *list.Model {
	if m.tab == 0 {
		return &m.sessions
	}
	return &m.trash
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		bodyH := msg.Height - 4 // header + status lines
		if bodyH < 1 {
			bodyH = 1
		}
		m.sessions.SetSize(msg.Width, bodyH)
		m.trash.SetSize(msg.Width, bodyH)
		m.viewport.Width = msg.Width
		m.viewport.Height = bodyH
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeConfirm:
			return m.updateConfirm(msg)
		case modePreview:
			return m.updatePreview(msg)
		case modeInput:
			return m.updateInput(msg)
		default:
			return m.updateList(msg)
		}
	}

	// route non-key messages to the active widget
	var cmd tea.Cmd
	switch m.mode {
	case modePreview:
		m.viewport, cmd = m.viewport.Update(msg)
	default:
		if m.tab == 0 {
			m.sessions, cmd = m.sessions.Update(msg)
		} else {
			m.trash, cmd = m.trash.Update(msg)
		}
	}
	return m, cmd
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	lst := m.activeList()
	// while typing a filter, let the list consume every key
	if lst.SettingFilter() {
		var cmd tea.Cmd
		*lst, cmd = lst.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.tab = 1 - m.tab
		m.status = ""
		return m, nil
	case "enter":
		return m.openPreview()
	case "d":
		if m.tab == 0 {
			return m.confirmPurge()
		}
	case "r":
		if m.tab == 0 {
			return m.startRename()
		}
		return m.confirmRecover()
	case "x":
		if m.tab == 1 {
			return m.confirmEmpty()
		}
	case "D":
		if m.tab == 1 {
			return m.confirmDeleteOne()
		}
	}

	var cmd tea.Cmd
	*lst, cmd = lst.Update(msg)
	return m, cmd
}

func (m model) updatePreview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.mode = modeList
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		status, err := m.confirm.action(&m)
		if err != nil {
			m.status = "✗ " + err.Error()
		} else {
			m.status = status
		}
		m.mode = modeList
		m.reload()
		return m, nil
	case "n", "N", "esc", "ctrl+c":
		m.mode = modeList
		m.status = "cancelled"
		return m, nil
	}
	return m, nil
}

// ---- actions --------------------------------------------------------------

func (m model) openPreview() (tea.Model, tea.Cmd) {
	var path, title string
	if m.tab == 0 {
		it, ok := m.sessions.SelectedItem().(sessionItem)
		if !ok {
			return m, nil
		}
		path, title = it.s.Path, it.Title()
	} else {
		it, ok := m.trash.SelectedItem().(trashItem)
		if !ok {
			return m, nil
		}
		path, title = it.t.JSONLPath, it.Title()
	}
	content := previewHdr.Render(title) + "\n" + statusStyle.Render(path) + "\n\n" + RenderTranscript(path, 200*1024)
	m.viewport.SetContent(content)
	m.viewport.GotoTop()
	m.mode = modePreview
	return m, nil
}

func (m model) startRename() (tea.Model, tea.Cmd) {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	m.rename = it.s
	m.input.SetValue(it.s.Title)
	m.input.CursorEnd()
	m.input.Focus()
	m.input.Width = m.width - 12
	m.mode = modeInput
	m.status = ""
	return m, textinput.Blink
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		title := strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.input.Blur()
		if title == "" || title == m.rename.Title {
			m.status = "rename cancelled"
			return m, nil
		}
		if err := m.cfg.Rename(m.rename, title); err != nil {
			m.status = "✗ " + err.Error()
		} else if m.rename.Live {
			m.status = "✓ renamed → " + title + "  ⚠ session is LIVE; the running app may overwrite it"
		} else {
			m.status = "✓ renamed → " + title + "  (visible in claude --resume)"
		}
		m.reload()
		return m, nil
	case "esc", "ctrl+c":
		m.mode = modeList
		m.input.Blur()
		m.status = "rename cancelled"
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) confirmPurge() (tea.Model, tea.Cmd) {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	s := it.s
	m.confirm = confirmState{
		prompt: fmt.Sprintf("Move this session to trash?  %s", it.Title()),
		action: func(m *model) (string, error) {
			base, err := m.cfg.Purge(s)
			if err != nil {
				return "", err
			}
			return "✓ trashed → " + base, nil
		},
	}
	m.mode = modeConfirm
	return m, nil
}

func (m model) confirmRecover() (tea.Model, tea.Cmd) {
	it, ok := m.trash.SelectedItem().(trashItem)
	if !ok {
		return m, nil
	}
	t := it.t
	m.confirm = confirmState{
		prompt: fmt.Sprintf("Recover to %s ?", shortProject(t.Origin)),
		action: func(m *model) (string, error) {
			dest, err := m.cfg.Recover(t)
			if err != nil {
				return "", err
			}
			return "✓ recovered → " + dest, nil
		},
	}
	m.mode = modeConfirm
	return m, nil
}

func (m model) confirmDeleteOne() (tea.Model, tea.Cmd) {
	it, ok := m.trash.SelectedItem().(trashItem)
	if !ok {
		return m, nil
	}
	t := it.t
	m.confirm = confirmState{
		prompt: fmt.Sprintf("PERMANENTLY delete this trashed session?  %s", it.Title()),
		action: func(m *model) (string, error) {
			if err := m.cfg.DeleteOne(t); err != nil {
				return "", err
			}
			return "✓ deleted " + t.Base, nil
		},
	}
	m.mode = modeConfirm
	return m, nil
}

func (m model) confirmEmpty() (tea.Model, tea.Cmd) {
	m.confirm = confirmState{
		prompt: "PERMANENTLY empty the entire session trash?",
		action: func(m *model) (string, error) {
			n, err := m.cfg.Empty()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("✓ emptied trash (%d session(s))", n), nil
		},
	}
	m.mode = modeConfirm
	return m, nil
}

// ---- view -----------------------------------------------------------------

func (m model) View() string {
	tabs := m.renderTabs()
	var body, footer string

	switch m.mode {
	case modePreview:
		body = m.viewport.View()
		footer = statusStyle.Render("↑/↓ scroll · esc/q back")
	case modeConfirm:
		if m.tab == 0 {
			body = m.sessions.View()
		} else {
			body = m.trash.View()
		}
		footer = confirmBox.Render(m.confirm.prompt + "   [y/N]")
	case modeInput:
		body = m.sessions.View()
		hint := "enter save · esc cancel"
		if m.rename.Live {
			hint = confirmBox.Render("⚠ LIVE session — the running app may overwrite this; rename closed sessions") + "\n" + statusStyle.Render(hint)
		} else {
			hint = statusStyle.Render(hint)
		}
		footer = m.input.View() + "\n" + hint
	default:
		if m.tab == 0 {
			body = m.sessions.View()
			footer = m.keyHints("enter preview · r rename · d trash · / filter · tab switch · q quit")
		} else {
			body = m.trash.View()
			footer = m.keyHints("enter preview · r recover · D delete · x empty · tab switch · q quit")
		}
		if m.status != "" {
			footer = statusStyle.Render(m.status) + "\n" + footer
		}
	}
	return tabs + "\n" + body + "\n" + footer
}

func (m model) renderTabs() string {
	render := func(idx int, label string) string {
		if m.tab == idx {
			return tabActive.Render(label)
		}
		return tabInactive.Render(label)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, render(0, "Sessions"), render(1, "Trash"))
}

func (m model) keyHints(s string) string { return statusStyle.Render(s) }

// ---- helpers --------------------------------------------------------------

func shortProject(p string) string {
	if home, err := homeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
