package tui

import (
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ashankgupta/claudefinder/internal/action"
	"github.com/ashankgupta/claudefinder/internal/claude"
	"github.com/ashankgupta/claudefinder/internal/index"
	"github.com/charmbracelet/lipgloss"
)

var (
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	faint = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	brand = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	rule  = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))

	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("74"))
	label  = lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
	proj   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	branch = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	count  = lipgloss.NewStyle().Foreground(lipgloss.Color("73"))
	warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errSty = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okSty  = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	title  = lipgloss.NewStyle().Bold(true)
	badge  = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	hit    = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("222")).Bold(true)

	ageNow  = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	ageDay  = lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	ageWeek = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	ageOld  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	ribbon    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
	ribbonKey = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Background(lipgloss.Color("236"))
	logoSty   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("55")).Bold(true)
	statSty   = lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Background(lipgloss.Color("236"))
	spinSty   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Background(lipgloss.Color("236")).Bold(true)
	frame     = lipgloss.NewStyle().Foreground(lipgloss.Color("60"))

	queryText  = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("60")).Bold(true)
	queryEmpty = lipgloss.NewStyle().Foreground(lipgloss.Color("146")).Background(lipgloss.Color("60")).Italic(true)
	cursorSty  = lipgloss.NewStyle().Foreground(lipgloss.Color("60")).Background(lipgloss.Color("214")).Bold(true)

	projectColors = []string{"39", "78", "214", "170", "80", "180", "141", "115", "209", "111", "150", "176"}

	roleUser = lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("78")).Bold(true)
	roleBot  = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("62")).Bold(true)

	selRow = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("61")).Bold(true)
	selBar = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
)

func projectStyle(dir string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(dir))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(projectColors[h.Sum32()%uint32(len(projectColors))]))
}

func ageStyle(t time.Time) lipgloss.Style {
	switch d := time.Since(t); {
	case d < 2*time.Hour:
		return ageNow
	case d < 24*time.Hour:
		return ageDay
	case d < 7*24*time.Hour:
		return ageWeek
	}
	return ageOld
}

const (
	bar      = "▌"
	rail     = "│"
	dot      = "●"
	spinner  = `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`
	chromeH  = 4
	detailAt = 96
)

func (m Model) View() string {
	if !m.ready {
		return ""
	}
	switch m.mode {
	case modeHelp:
		return m.helpView()
	case modePreview:
		return m.previewView()
	}
	return m.listView()
}

func (m Model) layout() (body, listW, detailW int) {
	body = max(1, m.height-chromeH)
	inner := max(20, m.width-4)
	if m.width >= detailAt {
		detailW = inner / 3
	}
	listW = inner - detailW
	if detailW > 0 {
		listW -= 3
	}
	return body, max(10, listW), detailW
}

func (m Model) rescroll() Model {
	body, listW, _ := m.layout()
	lines, cursorLine, cursorHeight := m.listLines(listW)
	m.offset = scrollTo(m.offset, cursorLine, cursorHeight, body, len(lines))
	return m
}

func (m Model) listView() string {
	body, listW, detailW := m.layout()

	lines, _, _ := m.listLines(listW)
	visible := window(lines, m.offset, body)
	for len(visible) < body {
		visible = append(visible, "")
	}

	cols := []string{lipgloss.NewStyle().Width(listW).Render(strings.Join(visible, "\n"))}
	if detailW > 0 {
		cols = append(cols,
			" ",
			strings.TrimRight(strings.Repeat(frame.Render(rail)+"\n", body), "\n"),
			lipgloss.NewStyle().Width(detailW).MaxHeight(body).PaddingLeft(1).
				Render(m.detail(detailW-2)))
	}
	pane := lipgloss.JoinHorizontal(lipgloss.Top, cols...)

	rows := []string{m.topEdge()}
	for _, l := range strings.Split(pane, "\n") {
		rows = append(rows, frame.Render(rail)+" "+padTo(l, m.width-4)+" "+frame.Render(rail))
	}
	rows = append(rows, frame.Render("╰"+strings.Repeat("─", max(0, m.width-2))+"╯"))

	return strings.Join(append([]string{m.header()}, append(rows, m.statusBar())...), "\n")
}

func (m Model) topEdge() string {
	right := ""
	if len(m.rows) > 0 {
		by := "time"
		if m.groupBy == groupDir {
			by = "dir"
		}
		right = badge.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.rows))) +
			faint.Render(" · ") + label.Render("group:"+by)
	}

	prefix := frame.Render("╭─") + " "
	icon := badge.Render("🔎 ")
	tail := frame.Render("─╮")
	if right != "" {
		tail = " " + right + " " + tail
	}

	budget := max(8, m.width-lipgloss.Width(prefix)-lipgloss.Width(icon)-lipgloss.Width(tail))

	var mid string
	switch {
	case m.searching:

		m.input.Width = budget - 1
		mid = icon + m.input.View()
	case m.input.Value() != "":
		note := fmt.Sprintf("  %d results · esc clears", len(m.rows))
		mid = icon + hit.Render(truncate(m.input.Value(), budget-len(note))) + faint.Render(note)
	default:
		mid = icon + faint.Render("press / to search")
	}

	line := prefix + mid
	if fill := m.width - lipgloss.Width(line) - lipgloss.Width(tail); fill > 0 {
		line += " " + frame.Render(strings.Repeat("─", fill-1))
	}
	return truncateStyled(line+tail, m.width)
}

func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return truncateStyled(s, w)
}

func (m Model) listLines(w int) (lines []string, cursorLine, cursorHeight int) {
	if len(m.rows) == 0 {
		return m.emptyLines(), 0, 1
	}
	lastGroup := ""
	for i, r := range m.rows {
		if g, ok := m.headerFor(r); ok && g != lastGroup {
			if lastGroup != "" {
				lines = append(lines, "")
			}
			lines = append(lines, sectionHeader(g, r.ProjectDir, w))
			lastGroup = g
		}
		start := len(lines)
		lines = append(lines, m.rowLines(r, i == m.cursor, w)...)
		if i == m.cursor {
			cursorLine, cursorHeight = start, len(lines)-start
		}
	}
	return lines, cursorLine, cursorHeight
}

func sectionHeader(name, dir string, w int) string {
	pc := projectStyle(dir)
	name = truncate(name, w-6)
	leaf := pc.Bold(true)
	styled := leaf.Render(name)
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		styled = faint.Render(name[:i+1]) + leaf.Render(name[i+1:])
	}
	styled = strings.Replace(styled, "⚠ missing", warn.Render("⚠ missing"), 1)
	styled = pc.Render(dot) + " " + styled

	fill := w - lipgloss.Width(name) - 3
	if fill < 1 {
		return styled
	}
	return styled + " " + rule.Render(strings.Repeat("─", fill-1))
}

func (m Model) headerFor(r index.Row) (string, bool) {
	if m.groupBy == groupDir {
		h := action.Tilde(r.ProjectDir)
		if !dirExists(r.ProjectDir) {
			h += "  ⚠ missing"
		}
		return h, true
	}
	if m.input.Value() != "" {
		return "", false
	}
	return groupOf(r.UpdatedAt), true
}

func sortRows(rows []index.Row, by grouping) []index.Row {
	if by != groupDir {
		return rows
	}
	latest := map[string]time.Time{}
	for _, r := range rows {
		if r.UpdatedAt.After(latest[r.ProjectDir]) {
			latest[r.ProjectDir] = r.UpdatedAt
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.ProjectDir != b.ProjectDir {
			if la, lb := latest[a.ProjectDir], latest[b.ProjectDir]; !la.Equal(lb) {
				return la.After(lb)
			}
			return a.ProjectDir < b.ProjectDir
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	return rows
}

func (m Model) rowLines(r index.Row, cur bool, w int) []string {
	name := r.Title
	if name == "" {
		name = "Untitled session"
	}
	age := relTime(r.UpdatedAt)
	msgs := fmt.Sprintf("%d msgs", r.MessageCount)
	meta := age + " · " + msgs

	pc := projectStyle(r.ProjectDir)
	gutter := pc.Render(rail) + " "
	if cur {
		gutter = selBar.Render(bar) + " "
	}
	inner := w - lipgloss.Width(gutter)

	nameW := inner - len(meta) - 2
	head := truncate(name, nameW)
	pad := max(1, inner-lipgloss.Width(head)-len(meta))
	var headStyled string
	if cur {
		headStyled = selRow.Render(head + strings.Repeat(" ", pad) + meta)
	} else {
		headStyled = m.mark(head, title) + strings.Repeat(" ", pad) +
			ageStyle(r.UpdatedAt).Render(age) + faint.Render(" · ") + count.Render(msgs)
	}
	out := []string{gutter + headStyled}

	if m.groupBy != groupDir {
		path := action.Shorten(r.ProjectDir, inner-2)
		line := accent.Render(truncate(path, inner))
		if !dirExists(r.ProjectDir) {
			line += warn.Render("  ⚠ missing")
		}
		out = append(out, gutter+line)
	}
	if r.Snippet != "" {
		out = append(out, gutter+badge.Render("↳ ")+m.mark(truncate(r.Snippet, inner-2), dim))
	}
	return out
}

func (m Model) mark(s string, base lipgloss.Style) string {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return base.Render(s)
	}
	lower := strings.ToLower(s)

	terms := strings.Fields(strings.ToLower(q))
	sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })

	marked := make([]bool, len(s))
	for _, t := range terms {
		if len(t) < 2 {
			continue
		}
		for at := 0; ; {
			i := strings.Index(lower[at:], t)
			if i < 0 {
				break
			}
			for k := at + i; k < at+i+len(t); k++ {
				marked[k] = true
			}
			at += i + len(t)
		}
	}

	var b strings.Builder
	for i := 0; i < len(s); {
		if !marked[i] {
			j := i
			for j < len(s) && !marked[j] {
				j++
			}
			b.WriteString(base.Render(s[i:j]))
			i = j
			continue
		}
		j := i
		for j < len(s) && marked[j] {
			j++
		}
		b.WriteString(hit.Render(s[i:j]))
		i = j
	}
	return b.String()
}

func (m Model) emptyLines() []string {
	if m.scanErr != nil {
		return []string{
			errSty.Render("Claude Code data directory not found."),
			"",
			dim.Render("Expected: " + action.Tilde(m.root) + "/projects/"),
			"",
			dim.Render("Run Claude Code at least once, then press R to rescan."),
		}
	}
	if m.indexing {
		return []string{badge.Render(m.spin()) + dim.Render(" Indexing sessions...")}
	}
	if m.input.Value() != "" {
		return []string{dim.Render("No sessions match ") + hit.Render(m.input.Value())}
	}
	return []string{
		"No Claude Code sessions found.",
		"",
		dim.Render("Press R to rescan."),
	}
}

func (m Model) detail(w int) string {
	s := m.selected()
	if s == nil || w < 10 {
		return ""
	}
	field := func(name, value string, sty lipgloss.Style) string {
		if value == "" {
			return ""
		}
		return label.Render(name) + "\n" + sty.Render(wrap(value, w)) + "\n\n"
	}
	plain := lipgloss.NewStyle()

	var b strings.Builder
	b.WriteString(brand.Render(wrap(s.Title, w)) + "\n\n")
	b.WriteString(field("PROJECT", action.Tilde(s.ProjectDir), accent))
	b.WriteString(field("BRANCH", s.GitBranch, branch))
	b.WriteString(field("LAST ACTIVE",
		relTime(s.UpdatedAt)+" · "+s.UpdatedAt.Local().Format("Jan 2 15:04"), ageStyle(s.UpdatedAt)))
	b.WriteString(field("MESSAGES", fmt.Sprint(s.MessageCount), count))
	b.WriteString(field("FIRST PROMPT", `"`+truncate(s.FirstPrompt, w*4)+`"`, plain))
	return b.String()
}

func (m Model) header() string {
	logo := logoSty.Render(" claudefinder ")

	projects := map[string]bool{}
	msgs := 0
	for _, r := range m.rows {
		projects[r.ProjectDir] = true
		msgs += r.MessageCount
	}
	stats := ribbon.Render("  ") +
		statSty.Render(fmt.Sprint(len(m.rows))) + ribbon.Render(" sessions") +
		ribbon.Render("  ·  ") +
		statSty.Render(fmt.Sprint(len(projects))) + ribbon.Render(" projects") +
		ribbon.Render("  ·  ") +
		statSty.Render(kilo(msgs)) + ribbon.Render(" messages")

	right := ribbonKey.Render("? help  q quit  ")
	if m.indexing {
		right = spinSty.Render(m.spin()+" indexing") + ribbon.Render("   ") + right
	}

	fill := m.width - lipgloss.Width(logo) - lipgloss.Width(stats) - lipgloss.Width(right)
	if fill < 1 {
		return padRibbon(logo, m.width)
	}
	return logo + stats + ribbon.Render(strings.Repeat(" ", fill)) + right
}

func kilo(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func padRibbon(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + ribbon.Render(strings.Repeat(" ", d))
	}
	return truncateStyled(s, w)
}

func (m Model) searchBar() string {
	logo := brand.Render("claudefinder")
	right := ""
	switch {
	case m.input.Value() != "" && !m.searching:
		right = fmt.Sprintf("%d results · esc to clear", len(m.rows))
	case len(m.rows) > 0:
		right = fmt.Sprintf("%d sessions", len(m.rows))
	}

	var mid string
	if m.searching {
		mid = badge.Render("🔎 ") + m.input.View()
	} else if q := m.input.Value(); q != "" {
		mid = badge.Render("🔎 ") + hit.Render(q)
	} else {
		mid = faint.Render("🔎 press / to search")
	}

	pad := m.width - lipgloss.Width(logo) - lipgloss.Width(mid) - len(right) - 4
	if pad < 1 {
		return truncateStyled(logo+"  "+mid, m.width)
	}
	return logo + "  " + mid + strings.Repeat(" ", pad) + count.Render(right) + "  "
}

func (m Model) statusBar() string {
	if m.scanErr != nil {
		return padRibbon(errSty.Background(lipgloss.Color("236")).
			Render(" scan failed: "+m.scanErr.Error()+" — press R to retry"), m.width)
	}
	if m.status != "" {
		sty := okSty.Background(lipgloss.Color("236"))
		if m.statusIsErr {
			sty = errSty.Background(lipgloss.Color("236"))
		}
		return padRibbon(sty.Render(" "+truncateStyled(m.status, m.width-2)), m.width)
	}

	type hint struct{ key, act string }
	hints := []hint{{"↑↓", "nav"}, {"⏎", "resume"}, {"/", "search"}, {"p", "preview"},
		{"s", "group"}, {"o", "open"}, {"c/y", "copy"}, {"R", "rescan"}, {"g/G", "top/end"}}

	out, w := " ", 1
	for _, h := range hints {
		seg := len(h.key) + len(h.act) + 3
		if w+seg > m.width-1 {
			break
		}
		out += badge.Background(lipgloss.Color("236")).Render(h.key) +
			ribbonKey.Render(" "+h.act+"  ")
		w += seg
	}
	return padRibbon(out, m.width)
}

func (m Model) spin() string {
	frames := []rune(spinner)
	return string(frames[m.frame%len(frames)])
}

func (m Model) previewView() string {
	s := m.selected()
	if s == nil {
		return ""
	}

	pc := projectStyle(s.ProjectDir)
	lead := frame.Render("╭─") + " " + pc.Render(action.Tilde(s.ProjectDir)) +
		faint.Render(" · ") + dim.Render(s.ID) +
		faint.Render(" · ") + count.Render(fmt.Sprint(s.MessageCount)+" messages") + " "
	tail := " " + badge.Render(fmt.Sprintf("%.0f%%", m.preview.ScrollPercent()*100)) + " " + frame.Render("─╮")
	fill := m.width - lipgloss.Width(lead) - lipgloss.Width(tail)
	top := frame.Render("╭" + strings.Repeat("─", max(0, m.width-2)) + "╮")
	if fill >= 1 {
		top = lead + frame.Render(strings.Repeat("─", fill)) + tail
	}

	rows := []string{top}
	for _, l := range strings.Split(m.preview.View(), "\n") {
		rows = append(rows, frame.Render(rail)+" "+padTo(l, m.width-4)+" "+frame.Render(rail))
	}
	rows = append(rows, frame.Render("╰"+strings.Repeat("─", max(0, m.width-2))+"╯"))

	head := padRibbon(logoSty.Render(" claudefinder ")+ribbon.Render("  ")+
		ribbon.Bold(true).Render(truncateStyled(s.Title, max(10, m.width-20))), m.width)

	foot := " " + badge.Background(lipgloss.Color("236")).Render("↑↓") + ribbonKey.Render(" scroll  ") +
		badge.Background(lipgloss.Color("236")).Render("⏎") + ribbonKey.Render(" resume  ") +
		badge.Background(lipgloss.Color("236")).Render("o") + ribbonKey.Render(" open dir  ") +
		badge.Background(lipgloss.Color("236")).Render("c/y") + ribbonKey.Render(" copy  ") +
		badge.Background(lipgloss.Color("236")).Render("esc") + ribbonKey.Render(" back  ")
	if m.status != "" {
		sty := okSty.Background(lipgloss.Color("236"))
		if m.statusIsErr {
			sty = errSty.Background(lipgloss.Color("236"))
		}
		foot = sty.Render(" " + truncateStyled(m.status, m.width-2))
	}

	return strings.Join(append([]string{head}, append(rows, padRibbon(foot, m.width))...), "\n")
}

func (m Model) renderConversation(msgs []claude.Message) string {
	if len(msgs) == 0 {
		return dim.Render("No conversation text indexed for this session.")
	}
	w := max(20, m.width-8)
	var b strings.Builder
	for i, msg := range msgs {
		chip, sty, railSty := " ASSISTANT ", roleBot, badge
		if msg.Role == "user" {
			chip, sty, railSty = " YOU ", roleUser, okSty
		}
		stamp := ""
		if !msg.Timestamp.IsZero() {
			stamp = faint.Render("  " + msg.Timestamp.Local().Format("Jan 2 15:04"))
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(railSty.Render(bar) + " " + sty.Render(chip) + stamp + "\n")
		for _, line := range strings.Split(wrap(msg.Content, w), "\n") {
			b.WriteString(railSty.Render(rail) + "  " + line + "\n")
		}
	}
	return b.String()
}

func (m Model) helpView() string {
	rows := [][2]string{
		{"j / ↓, k / ↑", "move"},
		{"ctrl+d / ctrl+u", "page down / up"},
		{"g / G", "first / last"},
		{"/", "search (metadata and conversation contents)"},
		{"tab", "keep the search, leave the search box"},
		{"esc", "clear search / go back"},
		{"enter / r", "resume this session in Claude Code"},
		{"p", "preview the conversation"},
		{"s", "group by project directory / by time"},
		{"o", "open project directory"},
		{"c", "copy project path"},
		{"y", "copy session ID"},
		{"R", "rescan"},
		{"?", "this help"},
		{"q / ctrl+c", "quit"},
	}

	w := max(40, min(m.width, 74))

	var body []string
	for _, r := range rows {
		key := r[0] + strings.Repeat(" ", max(1, 18-len([]rune(r[0]))))
		body = append(body, badge.Render(key)+dim.Render(r[1]))
	}
	body = append(body,
		"",
		label.Render("Claude data  ")+accent.Render(action.Tilde(m.root)),
		label.Render("Index        ")+accent.Render(action.Shorten(m.dbPath, w-19)),
		"",
		okSty.Render("Everything stays on this machine. No network, no telemetry."),
	)

	out := []string{frame.Render("╭─") + " " + brand.Render("keys") + " " +
		frame.Render(strings.Repeat("─", max(0, w-9))+"╮")}
	for _, l := range body {
		out = append(out, frame.Render(rail)+" "+padTo(l, w-4)+" "+frame.Render(rail))
	}
	out = append(out, frame.Render("╰"+strings.Repeat("─", max(0, w-2))+"╯"))

	head := padRibbon(logoSty.Render(" claudefinder ")+
		ribbon.Render("  find your Claude Code sessions"), m.width)
	return strings.Join(append([]string{head, ""}, append(out,
		"", faint.Render(" press any key to go back"))...), "\n")
}

func scrollTo(offset, cursorLine, cursorHeight, body, total int) int {
	if cursorLine < offset {
		offset = cursorLine
	}
	if end := cursorLine + cursorHeight; end > offset+body {
		offset = end - body
	}
	if maxOff := max(0, total-body); offset > maxOff {
		offset = maxOff
	}
	return max(0, offset)
}

func window(lines []string, offset, n int) []string {
	if offset > len(lines) {
		offset = len(lines)
	}
	end := min(offset+n, len(lines))
	return lines[offset:end]
}

func groupOf(t time.Time) string {
	now := time.Now()
	day := now.Truncate(24 * time.Hour)
	switch {
	case !t.Before(day):
		return "TODAY"
	case !t.Before(day.AddDate(0, 0, -1)):
		return "YESTERDAY"
	case !t.Before(day.AddDate(0, 0, -7)):
		return "THIS WEEK"
	case !t.Before(day.AddDate(0, 0, -30)):
		return "THIS MONTH"
	}
	return "EARLIER"
}

func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("Jan 2 2006")
}

func truncate(s string, w int) string {
	if w <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func truncateStyled(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func wrap(s string, w int) string {
	if w < 4 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

var dirCache = map[string]bool{}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	if ok, seen := dirCache[path]; seen {
		return ok
	}
	fi, err := os.Stat(path)
	ok := err == nil && fi.IsDir()
	dirCache[path] = ok
	return ok
}

