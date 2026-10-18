// Package tui provides a read-only terminal browser for any Meads task store.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/jpillora/meads/pkg/meads"
)

// Reader deliberately excludes mutations: browsing cannot change tasks.
type Reader interface {
	Get([]int) ([]meads.Task, error)
	Revision() (string, error)
}

func Run(store Reader) error {
	_, err := tea.NewProgram(newModel(store), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

var (
	background = lipgloss.Color("#0d1117")
	foreground = lipgloss.Color("#e6edf3")
	muted      = lipgloss.Color("#8b949e")
	border     = lipgloss.Color("#30363d")
	accent     = lipgloss.Color("#2f81f7")
	card       = lipgloss.Color("#161b22")
	success    = lipgloss.Color("#3fb950")
	warning    = lipgloss.Color("#d29922")
)

func clean(s string) string {
	// Stored task text is untrusted terminal content, including ANSI/OSC escapes.
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func oneLine(s string) string { return strings.Join(strings.Fields(clean(s)), " ") }
func priority(t meads.Task) string {
	if p, err := meads.NormalizePriority(t.Priority); err == nil {
		return p
	}
	return "P2"
}
func status(t meads.Task) string {
	if t.Status == "" {
		return "open"
	}
	return t.Status
}
func unmet(t meads.Task, all map[int]meads.Task) []int {
	var ids []int
	for _, id := range t.DependsOn {
		if p, ok := all[id]; ok && !p.Deleted && status(p) != "closed" {
			ids = append(ids, id)
		}
	}
	return ids
}
func group(t meads.Task, all map[int]meads.Task) int {
	if status(t) == "inprogress" {
		return 0
	}
	if status(t) == "open" && len(unmet(t, all)) == 0 {
		return 1
	}
	return 2
}

var groupNames = []string{"IN PROGRESS", "READY", "NOT READY"}
var groupColors = []lipgloss.Color{accent, success, warning}

// visibleTasks uses the complete snapshot to resolve dependencies before filtering.
func visibleTasks(tasks []meads.Task, query string) ([]meads.Task, map[int]meads.Task) {
	all := make(map[int]meads.Task, len(tasks))
	for _, t := range tasks {
		if !t.Deleted {
			all[t.ID] = t
		}
	}
	var visible []meads.Task
	for _, t := range tasks {
		if t.Deleted || status(t) == "closed" {
			continue
		}
		haystack := strings.ToLower(fmt.Sprintf("#%d %s %s %s %s %s %s", t.ID, t.Title, t.Description, status(t), t.Type, priority(t), strings.Join(t.Tags, " ")))
		matches := true
		for _, word := range strings.Fields(strings.ToLower(query)) {
			if !strings.Contains(haystack, word) {
				matches = false
				break
			}
		}
		if matches {
			visible = append(visible, t)
		}
	}
	sort.SliceStable(visible, func(i, j int) bool {
		a, b := visible[i], visible[j]
		if ga, gb := group(a, all), group(b, all); ga != gb {
			return ga < gb
		}
		if pa, pb := priority(a), priority(b); pa != pb {
			return pa < pb
		}
		return a.ID < b.ID
	})
	return visible, all
}

type loadedMsg struct {
	tasks     []meads.Task
	err       error
	revision  string
	unchanged bool
}
type tickMsg struct{}

func tick() tea.Cmd { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

type model struct {
	store                       Reader
	tasks, visible              []meads.Task
	all                         map[int]meads.Task
	selected, width, height     int
	list, detail                viewport.Model
	starts, ends                []int
	query                       string
	searching, details, loading bool
	loaded                      bool
	revision                    string
	err                         error
}

func newModel(store Reader) model {
	return model{store: store, loading: true, list: viewport.New(0, 0), detail: viewport.New(0, 0)}
}
func (m model) load() tea.Cmd {
	return func() tea.Msg {
		revision, err := m.store.Revision()
		if err != nil {
			return loadedMsg{err: err}
		}
		if m.loaded && revision == m.revision {
			return loadedMsg{unchanged: true}
		}
		tasks, err := m.store.Get(nil)
		return loadedMsg{tasks: tasks, err: err, revision: revision}
	}
}
func (m model) Init() tea.Cmd { return tea.Batch(m.load(), tick()) }
func (m *model) refilter() {
	id := 0
	if m.selected < len(m.visible) {
		id = m.visible[m.selected].ID
	}
	m.visible, m.all = visibleTasks(m.tasks, m.query)
	m.selected = min(m.selected, max(0, len(m.visible)-1))
	for i, t := range m.visible {
		if t.ID == id {
			m.selected = i
			break
		}
	}
}
func (m *model) resize() {
	w := max(1, m.width)
	if m.details && w >= 100 {
		m.list.Width = w * 45 / 100
		m.detail.Width = w - m.list.Width - 1
	} else {
		m.list.Width = w
		m.detail.Width = w
	}
	m.list.Height = max(1, m.height-4)
	m.detail.Height = m.list.Height
}
func (m *model) rebuild(resetDetail bool) {
	m.resize()
	var cards []string
	m.starts, m.ends = nil, nil
	line, lastGroup := 0, -1
	for i, t := range m.visible {
		g := group(t, m.all)
		if g != lastGroup {
			count := 0
			for _, other := range m.visible {
				if group(other, m.all) == g {
					count++
				}
			}
			cards = append(cards, lipgloss.NewStyle().Foreground(groupColors[g]).Bold(true).Render(fmt.Sprintf(" %s · %d", groupNames[g], count)))
			line++
			lastGroup = g
		}
		width := max(1, m.list.Width-4)
		taskType := t.Type
		if taskType == "" {
			taskType = "task"
		}
		meta := fmt.Sprintf("#%d  %s  %s  %s", t.ID, priority(t), taskType, status(t))
		if len(t.Tags) > 0 {
			meta += "  " + strings.Join(t.Tags, " · ")
		}
		if deps := unmet(t, m.all); len(deps) > 0 {
			meta += fmt.Sprintf("  waiting on %v", deps)
		}
		body := lipgloss.NewStyle().Foreground(muted).Render(ansi.Truncate(oneLine(meta), width, "…")) + "\n" +
			lipgloss.NewStyle().Bold(true).Render(ansi.Truncate(oneLine(t.Title), width, "…"))
		if desc := oneLine(t.Description); desc != "" {
			body += "\n" + lipgloss.NewStyle().Foreground(muted).Render(ansi.Truncate(desc, width, "…"))
		}
		color := border
		if i == m.selected {
			color = accent
		}
		rendered := lipgloss.NewStyle().Width(max(1, m.list.Width-2)).Padding(0, 1).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(color).Background(card).Render(body)
		m.starts = append(m.starts, line)
		line += lipgloss.Height(rendered)
		m.ends = append(m.ends, line)
		cards = append(cards, rendered, "")
		line++
	}
	if len(cards) == 0 {
		message := "No active tasks. Run md add to create one."
		if m.query != "" {
			message = "No tasks match your search."
		}
		if !m.loaded {
			message = "Loading tasks…"
			if m.err != nil {
				message = "Unable to load tasks. Press r to retry."
			}
		}
		cards = []string{lipgloss.NewStyle().Foreground(muted).Width(m.list.Width).Render(message)}
	}
	m.list.SetContent(strings.Join(cards, "\n"))
	m.ensureSelected()
	if m.details {
		m.renderDetail(resetDetail)
	}
}
func (m *model) ensureSelected() {
	if m.selected >= len(m.starts) {
		return
	}
	top, bottom := m.starts[m.selected], m.ends[m.selected]
	if top < m.list.YOffset {
		m.list.SetYOffset(top)
	}
	if bottom > m.list.YOffset+m.list.Height {
		m.list.SetYOffset(min(top, bottom-m.list.Height))
	}
}
func (m *model) renderDetail(reset bool) {
	offset := m.detail.YOffset
	if reset {
		offset = 0
	}
	if len(m.visible) == 0 {
		m.detail.SetContent("No task selected.")
		return
	}
	t := m.visible[m.selected]
	width := max(1, m.detail.Width-4)
	var b strings.Builder
	fmt.Fprintf(&b, "# #%d %s\n\n%s · %s · %s\n\n", t.ID, oneLine(t.Title), priority(t), oneLine(t.Type), oneLine(status(t)))
	if len(t.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n\n", oneLine(strings.Join(t.Tags, ", ")))
	}
	if t.StatusReason != "" {
		fmt.Fprintf(&b, "Reason: %s\n\n", clean(t.StatusReason))
	}
	if t.AgentID != "" {
		fmt.Fprintf(&b, "Agent: %s\n\n", oneLine(t.AgentID))
	}
	if len(t.FilesInScope) > 0 {
		fmt.Fprintf(&b, "Files: %s\n\n", oneLine(strings.Join(t.FilesInScope, ", ")))
	}
	b.WriteString(clean(t.Description))
	if len(t.DependsOn) > 0 {
		b.WriteString("\n\n## Dependencies\n\n")
		for _, id := range t.DependsOn {
			if p, ok := m.all[id]; ok {
				fmt.Fprintf(&b, "- #%d %s (%s)\n", id, oneLine(p.Title), oneLine(status(p)))
			} else {
				fmt.Fprintf(&b, "- #%d (missing; satisfied)\n", id)
			}
		}
	}
	if len(t.Meta) > 0 {
		keys := make([]string, 0, len(t.Meta))
		for k := range t.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\n\n## Metadata\n\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s: %s\n", oneLine(k), oneLine(t.Meta[k]))
		}
	}
	content := b.String()
	// Keep Markdown headings and links in the same palette as the task cards.
	style := styles.DarkStyleConfig
	fg, bg, blue, chip := string(foreground), string(background), string(accent), "#79c0ff"
	style.Document.Color = &fg
	style.Document.BackgroundColor = &bg
	style.Heading.Color = &blue
	style.H1.Color = &fg
	style.H1.BackgroundColor = &bg
	style.H6.Color = &blue
	style.Link.Color = &chip
	style.LinkText.Color = &chip
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err == nil {
		if rendered, e := renderer.Render(content); e == nil {
			content = rendered
		}
	}
	m.detail.SetContent(content)
	m.detail.SetYOffset(offset)
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil && !msg.unchanged {
			oldID := 0
			if m.selected < len(m.visible) {
				oldID = m.visible[m.selected].ID
			}
			m.loaded = true
			m.revision = msg.revision
			m.tasks = msg.tasks
			m.refilter()
			reset := m.selected < len(m.visible) && m.visible[m.selected].ID != oldID
			m.rebuild(reset)
		}
		if msg.err != nil && !m.loaded {
			m.rebuild(false)
		}
		return m, nil
	case tickMsg:
		if !m.loading {
			m.loading = true
			return m, tea.Batch(m.load(), tick())
		}
		return m, tick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild(false)
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.searching {
			switch key {
			case "esc", "enter":
				m.searching = false
			case "backspace", "ctrl+h":
				r := []rune(m.query)
				if len(r) > 0 {
					m.query = string(r[:len(r)-1])
				}
			case "ctrl+u":
				m.query = ""
			default:
				if msg.Type == tea.KeyRunes || key == " " {
					m.query += string(msg.Runes)
					if key == " " && len(msg.Runes) == 0 {
						m.query += " "
					}
				}
			}
			m.refilter()
			m.rebuild(true)
			return m, nil
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "/":
			m.searching = true
		case "esc":
			if m.details {
				m.details = false
			} else {
				m.query = ""
				m.refilter()
			}
			m.rebuild(true)
		case "enter", "tab":
			m.details = !m.details
			m.rebuild(true)
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.load()
			}
		case "j", "down", "k", "up", "g", "home", "G", "end":
			if key == "g" || key == "home" {
				m.selected = 0
			} else if key == "G" || key == "end" {
				m.selected = max(0, len(m.visible)-1)
			} else if key == "j" || key == "down" {
				m.selected = min(max(0, len(m.visible)-1), m.selected+1)
			} else {
				m.selected = max(0, m.selected-1)
			}
			m.rebuild(true)
		case "pgdown", "ctrl+d", "pgup", "ctrl+u":
			if m.details {
				if key == "pgdown" || key == "ctrl+d" {
					m.detail.HalfPageDown()
				} else {
					m.detail.HalfPageUp()
				}
			} else {
				step := max(1, m.list.Height/5)
				if key == "pgup" || key == "ctrl+u" {
					step = -step
				}
				m.selected = max(0, min(len(m.visible)-1, m.selected+step))
				m.rebuild(true)
			}
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		if m.details && (m.width < 100 || msg.X > m.list.Width) {
			m.detail, cmd = m.detail.Update(msg)
		} else {
			m.list, cmd = m.list.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}
func (m model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(" meads") + lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf(" / tasks · %d · read only", len(m.visible)))
	search := " / search"
	if m.query != "" || m.searching {
		search = " / " + oneLine(m.query)
		if m.searching {
			search += "▏"
		}
	}
	body := m.list.View()
	if m.details {
		if m.width >= 100 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("│\n", max(0, m.list.Height-1))+"│"), m.detail.View())
		} else {
			body = m.detail.View()
		}
	}
	footer := " ↑↓/jk select · enter details · / search · r refresh · q quit"
	if m.details {
		footer = " ↑↓/jk select · PgUp/PgDn scroll · esc back · q quit"
	}
	if m.searching {
		footer = " Type to filter · enter done · ctrl+u clear"
	}
	if m.err != nil {
		footer = " Refresh failed: " + oneLine(m.err.Error()) + " · r retry · q quit"
	}
	lines := strings.Split(header+"\n"+search+"\n"+body+"\n"+footer, "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, m.width, "")
	}
	return lipgloss.NewStyle().Width(m.width).Height(m.height).Foreground(foreground).Background(background).Render(strings.Join(lines, "\n"))
}
