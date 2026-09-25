package tui

import (
	"fmt"
	"time"

	"github.com/ashankgupta/claudefinder/internal/action"
	"github.com/ashankgupta/claudefinder/internal/claude"
	"github.com/ashankgupta/claudefinder/internal/index"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	listLimit   = 1000
	searchLimit = 200
)

type mode int

const (
	modeList mode = iota
	modePreview
	modeHelp
)

type grouping int

const (
	groupTime grouping = iota
	groupDir
)

type Model struct {
	db     *index.DB
	root   string
	dbPath string

	rows   []index.Row
	cursor int
	offset int

	mode      mode
	groupBy   grouping
	searching bool
	input     textinput.Model

	preview   viewport.Model
	previewID string

	indexing bool
	indexed  int
	frame    int
	scanErr  error

	keepID string

	status      string
	statusIsErr bool

	width, height int
	ready         bool
}

func New(db *index.DB, root, dbPath, query string) Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to search titles, paths and conversations..."
	in.PlaceholderStyle = queryEmpty
	in.TextStyle = queryText
	in.Cursor.Style = cursorSty
	in.SetValue(query)
	in.CursorEnd()

	return Model{
		db:        db,
		root:      root,
		dbPath:    dbPath,
		input:     in,
		groupBy:   groupDir,
		indexing:  true,
		searching: query != "",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.sync(), tick(), spin(), textinput.Blink)
}

type rowsMsg struct {
	rows []index.Row
	err  error
}
type syncMsg struct {
	stats index.Stats
	err   error
	done  bool
}
type messagesMsg struct {
	id   string
	msgs []claude.Message
	err  error
}
type statusMsg struct {
	text  string
	isErr bool
}
type clearStatusMsg struct{}

type resumedMsg struct{ err error }
type tickMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

type spinMsg struct{}

func spin() tea.Cmd {
	return tea.Tick(110*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (m Model) load() tea.Cmd {
	q := m.input.Value()
	limit := listLimit
	if q != "" {
		limit = searchLimit
	}
	db := m.db
	return func() tea.Msg {
		rows, err := db.Search(q, limit)
		return rowsMsg{rows: rows, err: err}
	}
}

func (m Model) sync() tea.Cmd {
	db, root := m.db, m.root
	return func() tea.Msg {
		st, err := db.Sync(root)
		return syncMsg{stats: st, err: err, done: true}
	}
}

func (m Model) loadMessages(id string) tea.Cmd {
	db := m.db
	return func() tea.Msg {
		msgs, err := db.Messages(id)
		return messagesMsg{id: id, msgs: msgs, err: err}
	}
}

func flash(format string, a ...any) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: fmt.Sprintf(format, a...)} }
}

func flashErr(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: err.Error(), isErr: true} }
}

func expireStatus() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if n, ok := next.(Model); ok {
		return n.rescroll(), cmd
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.preview.Width = max(10, msg.Width-4)
		m.preview.Height = max(1, msg.Height-4)
		m.input.Width = max(10, msg.Width-6)
		m.ready = true
		return m, nil

	case rowsMsg:
		if msg.err != nil {
			return m, flashErr(msg.err)
		}
		m.rows = sortRows(msg.rows, m.groupBy)
		if m.keepID != "" {
			for i, r := range m.rows {
				if r.ID == m.keepID {
					m.cursor = i
					break
				}
			}
			m.keepID = ""
		}
		if m.cursor >= len(m.rows) {
			m.cursor = max(0, len(m.rows)-1)
		}
		return m, nil

	case syncMsg:
		m.indexing = !msg.done
		m.indexed = msg.stats.Indexed
		m.scanErr = msg.err
		if msg.done && msg.err == nil && (msg.stats.Indexed > 0 || msg.stats.Removed > 0) {
			return m, m.load()
		}
		return m, nil

	case messagesMsg:
		if msg.err != nil {
			return m, flashErr(msg.err)
		}
		m.previewID = msg.id
		m.preview.SetContent(m.renderConversation(msg.msgs))
		m.preview.GotoTop()
		return m, nil

	case statusMsg:
		m.status, m.statusIsErr = msg.text, msg.isErr
		return m, expireStatus()

	case resumedMsg:
		if msg.err != nil {
			m.status, m.statusIsErr = "claude exited: "+msg.err.Error(), true
		}

		m.indexing = true
		return m, tea.Batch(m.sync(), tick(), spin(), expireStatus())

	case tickMsg:
		if m.indexing {
			return m, tea.Batch(m.load(), tick())
		}
		return m, nil

	case spinMsg:
		if m.indexing {
			m.frame++
			return m, spin()
		}
		return m, nil

	case clearStatusMsg:
		m.status = ""
		return m, nil

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.mode {
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modePreview:
		return m.previewKey(k)
	}

	if m.searching {
		switch k.String() {
		case "esc":
			m.searching = false
			m.input.SetValue("")
			m.input.Blur()
			m.cursor, m.offset = 0, 0
			return m, m.load()
		case "tab":
			m.searching = false
			m.input.Blur()
			return m, nil
		case "enter":
			return m.act(k)
		case "up", "down", "ctrl+n", "ctrl+p":

		default:
			var cmd tea.Cmd
			before := m.input.Value()
			m.input, cmd = m.input.Update(k)
			if m.input.Value() != before {
				m.cursor, m.offset = 0, 0
				return m, tea.Batch(cmd, m.load())
			}
			return m, cmd
		}
	}

	switch k.String() {
	case "q":
		return m, tea.Quit
	case "j", "down", "ctrl+n":
		m.move(1)
	case "k", "up", "ctrl+p":
		m.move(-1)
	case "ctrl+d", "pgdown":
		m.move(10)
	case "ctrl+u", "pgup":
		m.move(-10)
	case "g", "home":
		m.cursor, m.offset = 0, 0
	case "G", "end":
		m.cursor = max(0, len(m.rows)-1)
	case "/":
		m.searching = true
		m.input.Focus()
		return m, textinput.Blink
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.cursor, m.offset = 0, 0
			return m, m.load()
		}
	case "s":
		if m.groupBy == groupTime {
			m.groupBy = groupDir
		} else {
			m.groupBy = groupTime
		}
		if sel := m.selected(); sel != nil {
			m.keepID = sel.ID
		}
		return m, m.load()
	case "?":
		m.mode = modeHelp
	case "R":
		if !m.indexing {
			m.indexing = true
			dirCache = map[string]bool{}
			return m, tea.Batch(m.sync(), tick(), spin(), flash("Rescanning %s...", action.Tilde(m.root)))
		}
	case "p", "tab":
		if s := m.selected(); s != nil {
			m.mode = modePreview
			if m.previewID != s.ID {
				m.preview.SetContent("Loading conversation...")
			}
			return m, m.loadMessages(s.ID)
		}
	default:
		return m.act(k)
	}
	return m, nil
}

func (m Model) act(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.selected()
	if s == nil {
		return m, nil
	}
	switch k.String() {
	case "o":
		if err := action.OpenDirectory(s.ProjectDir); err != nil {
			return m, flashErr(err)
		}
		return m, flash("Opened %s", action.Tilde(s.ProjectDir))
	case "r", "enter":
		cmd, err := action.ResumeCommand(s.ID, s.ProjectDir)
		if err != nil {
			return m, flashErr(err)
		}

		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return resumedMsg{err: err}
		})
	case "c":
		if err := action.Copy(s.ProjectDir); err != nil {
			return m, flashErr(err)
		}
		return m, flash("Copied %s", action.Tilde(s.ProjectDir))
	case "y":
		if err := action.Copy(s.ID); err != nil {
			return m, flashErr(err)
		}
		return m, flash("Copied session ID %s", s.ID)
	}
	return m, nil
}

func (m Model) previewKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "p":
		m.mode = modeList
		return m, nil
	case "o", "r", "c", "y", "enter":
		return m.act(k)
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	var cmd tea.Cmd
	m.preview, cmd = m.preview.Update(k)
	return m, cmd
}

func (m *Model) move(n int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+n, 0), len(m.rows)-1)
}

func (m Model) selected() *index.Row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func Run(db *index.DB, root, dbPath, query string) error {
	p := tea.NewProgram(New(db, root, dbPath, query), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

