package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jpillora/meads/pkg/meads"
)

func fixture() []meads.Task {
	return []meads.Task{
		{ID: 1, Title: "Closed parent", Status: "closed"},
		{ID: 2, Title: "Ready low", Priority: "P3", DependsOn: []int{1}},
		{ID: 3, Title: "Working", Status: "inprogress", Priority: "P9", DependsOn: []int{4}},
		{ID: 4, Title: "Ready high", Priority: "P0"},
		{ID: 5, Title: "Waiting", Priority: "P0", DependsOn: []int{4}},
		{ID: 6, Title: "Draft", Status: "draft", Priority: "P1"},
		{ID: 7, Title: "Deleted parent", Deleted: true},
		{ID: 8, Title: "Ready missing parents", DependsOn: []int{7, 999}},
		{ID: 9, Title: "Manually blocked", Status: "blocked", Priority: "P2"},
	}
}
func TestOrderingAndReadiness(t *testing.T) {
	tasks, _ := visibleTasks(fixture(), "")
	var ids []int
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	if want := []int{3, 4, 8, 2, 5, 6, 9}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v; want %v", ids, want)
	}
	// Filtering must not hide the parent used to calculate readiness.
	tasks, all := visibleTasks(fixture(), "Waiting")
	if len(tasks) != 1 || group(tasks[0], all) != 2 {
		t.Fatalf("filtered task lost its blocking dependency: %v", tasks)
	}
}

func update(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}
func readyModel() model {
	m := newModel(nil)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	return update(m, loadedMsg{tasks: fixture(), revision: "1"})
}
func TestResponsiveViews(t *testing.T) {
	m := readyModel()
	m.tasks[2].Description = strings.Repeat("## Markdown\n\nLong description with 日本語 and emoji 🚀.\n\n", 25)
	m.refilter()
	for _, details := range []bool{false, true} {
		m.details = details
		for _, width := range []int{1, 2, 8, 25, 60, 99, 100, 160} {
			for _, height := range []int{1, 3, 8, 24} {
				m = update(m, tea.WindowSizeMsg{Width: width, Height: height})
				view := m.View()
				if got := lipgloss.Width(view); got > width {
					t.Fatalf("details=%v %dx%d: width %d", details, width, height, got)
				}
				if got := lipgloss.Height(view); got > height {
					t.Fatalf("details=%v %dx%d: height %d", details, width, height, got)
				}
			}
		}
	}
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 24})
	if m.list.Width+m.detail.Width+1 != 120 {
		t.Fatal("split pane widths do not fill terminal")
	}
	m = update(m, tea.WindowSizeMsg{Width: 60, Height: 24})
	if m.detail.Width != 60 {
		t.Fatal("narrow detail should fill terminal")
	}
}
func TestNavigationSearchAndRefresh(t *testing.T) {
	m := readyModel()
	m = update(m, tea.WindowSizeMsg{Width: 60, Height: 10})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.visible[m.selected].ID != 9 || m.list.YOffset == 0 {
		t.Fatal("last task not scrolled into view")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.details {
		t.Fatal("enter did not open details")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.details {
		t.Fatal("escape did not close details")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Ready high")})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.visible) != 1 || m.visible[0].ID != 4 {
		t.Fatalf("search failed: %v", m.visible)
	}
	m = update(m, loadedMsg{err: errors.New("offline")})
	if len(m.visible) != 1 || !strings.Contains(m.View(), "Refresh failed") {
		t.Fatal("refresh error discarded snapshot or was hidden")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = update(m, loadedMsg{tasks: append(fixture(), meads.Task{ID: 10, Title: "New", Priority: "P0"}), revision: "2"})
	if m.err != nil || m.visible[m.selected].ID != 4 {
		t.Fatal("refresh failed to preserve selected task")
	}
}

type reader struct {
	gets     int
	revision string
}

func (r *reader) Revision() (string, error)       { return r.revision, nil }
func (r *reader) Get([]int) ([]meads.Task, error) { r.gets++; return fixture(), nil }
func TestUnchangedRevisionSkipsTaskRead(t *testing.T) {
	r := &reader{revision: "1"}
	m := newModel(r)
	m = update(m, m.load()())
	m = update(m, m.load()())
	if r.gets != 1 {
		t.Fatalf("unchanged revision reread tasks %d times", r.gets)
	}
	r.revision = "2"
	m = update(m, m.load()())
	if r.gets != 2 || m.revision != "2" {
		t.Fatal("new revision did not reload")
	}
}
func TestTaskTextCannotEmitTerminalControls(t *testing.T) {
	malicious := "safe\x1b]52;c;c2VjcmV0\a\x1b[31mred\x1b[0m\x00\r"
	got := clean(malicious)
	if got != "safered" {
		t.Fatalf("unsafe task text: %q", got)
	}
	m := readyModel()
	m.tasks = []meads.Task{{ID: 1, Title: malicious, Description: malicious, Tags: meads.Tags{malicious}, Meta: map[string]string{malicious: malicious}}}
	m.details = true
	m.refilter()
	m.rebuild(true)
	if strings.Contains(m.View(), "]52;") {
		t.Fatal("OSC clipboard escape reached terminal")
	}
}
func TestLargeListSelectionRemainsVisible(t *testing.T) {
	m := readyModel()
	var tasks []meads.Task
	for i := 1; i <= 100; i++ {
		tasks = append(tasks, meads.Task{ID: i, Title: fmt.Sprintf("Task %d", i)})
	}
	m = update(m, loadedMsg{tasks: tasks, revision: "many"})
	for i := 0; i < 99; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyDown})
		if m.starts[m.selected] < m.list.YOffset || m.ends[m.selected] > m.list.YOffset+m.list.Height {
			t.Fatalf("selection %d outside viewport", m.selected)
		}
	}
}
