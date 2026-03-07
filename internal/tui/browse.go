// Package tui provides an interactive terminal UI for the Wasteland wanted board.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gastownhall/wasteland/internal/commons"
)

type browseModel struct {
	items         []commons.WantedSummary
	pendingIDs    map[string]int // wanted IDs with pending changes; value is PR count
	cursor        int
	statusIdx     int // index into statusCycle
	typeIdx       int // index into typeCycle
	priorityIdx   int // index into priorityCycle
	sortIdx       int // index into sortCycle
	myItems       bool
	searchMode    bool
	search        textinput.Model
	projectMode   bool
	project       textinput.Model
	projectFilter string // applied project value; decoupled from textinput state
	width         int
	height        int
	loading       bool
	err           error
}

func newBrowseModel() browseModel {
	ti := textinput.New()
	ti.Placeholder = "search title..."
	ti.CharLimit = 64

	pi := textinput.New()
	pi.Placeholder = "project name..."
	pi.CharLimit = 32

	return browseModel{
		statusIdx: 0, // default to "open"
		search:    ti,
		project:   pi,
		loading:   true,
	}
}

func (m browseModel) filter(rigHandle string) commons.BrowseFilter {
	f := commons.BrowseFilter{
		Status:   commons.ValidStatuses()[m.statusIdx],
		Type:     commons.ValidTypes()[m.typeIdx],
		Priority: commons.ValidPriorities()[m.priorityIdx],
		Limit:    100,
		Search:   m.search.Value(),
		Sort:     commons.ValidSortOrders()[m.sortIdx],
	}
	if m.projectFilter != "" {
		f.Project = m.projectFilter
	}
	if m.myItems && rigHandle != "" {
		f.MyItems = rigHandle
	}
	return f
}

func (m *browseModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *browseModel) setData(msg browseDataMsg) {
	m.loading = false
	m.err = msg.err
	m.items = msg.items
	m.pendingIDs = msg.pendingIDs
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
}

func (m browseModel) update(msg bubbletea.Msg, cfg Config) (browseModel, bubbletea.Cmd) {
	if m.searchMode {
		return m.updateSearch(msg, cfg)
	}
	if m.projectMode {
		return m.updateProject(msg, cfg)
	}

	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		switch {
		case key.Matches(msg, keys.Quit):
			return m, bubbletea.Quit

		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}

		case key.Matches(msg, keys.Down):
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}

		case key.Matches(msg, keys.Enter):
			if m.cursor < len(m.items) {
				item := m.items[m.cursor]
				return m, func() bubbletea.Msg {
					return navigateMsg{view: viewDetail, wantedID: item.ID}
				}
			}

		case key.Matches(msg, keys.Search):
			m.searchMode = true
			m.search.Focus()
			return m, textinput.Blink

		case key.Matches(msg, keys.Status):
			m.statusIdx = (m.statusIdx + 1) % len(commons.ValidStatuses())
			m.cursor = 0
			m.loading = true
			return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))

		case key.Matches(msg, keys.Type):
			m.typeIdx = (m.typeIdx + 1) % len(commons.ValidTypes())
			m.cursor = 0
			m.loading = true
			return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))

		case key.Matches(msg, keys.Priority):
			m.priorityIdx = (m.priorityIdx + 1) % len(commons.ValidPriorities())
			m.cursor = 0
			m.loading = true
			return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))

		case key.Matches(msg, keys.Project):
			m.projectMode = true
			m.project.SetValue(m.projectFilter)
			m.project.Focus()
			return m, textinput.Blink

		case key.Matches(msg, keys.MyItems):
			m.myItems = !m.myItems
			if m.myItems {
				// Reset status to "all" so the user sees all their items,
				// not just the ones matching the current status filter.
				m.statusIdx = len(commons.ValidStatuses()) - 1
			}
			m.cursor = 0
			m.loading = true
			return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))

		case key.Matches(msg, keys.Sort):
			m.sortIdx = (m.sortIdx + 1) % len(commons.ValidSortOrders())
			m.cursor = 0
			m.loading = true
			return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))

		case key.Matches(msg, keys.Me):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewMe}
			}

		case key.Matches(msg, keys.Settings):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewSettings}
			}
		}
	}

	return m, nil
}

func (m browseModel) updateSearch(msg bubbletea.Msg, cfg Config) (browseModel, bubbletea.Cmd) {
	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		switch msg.String() {
		case "enter", "esc":
			m.searchMode = false
			m.search.Blur()
			if msg.String() == "enter" {
				m.cursor = 0
				m.loading = true
				return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))
			}
			return m, nil
		}
	}

	var cmd bubbletea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}

func (m browseModel) updateProject(msg bubbletea.Msg, cfg Config) (browseModel, bubbletea.Cmd) {
	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		switch msg.String() {
		case "enter", "esc":
			m.projectMode = false
			m.project.Blur()
			if msg.String() == "enter" {
				m.projectFilter = m.project.Value()
				m.cursor = 0
				m.loading = true
				return m, fetchBrowse(cfg, m.filter(cfg.RigHandle))
			}
			return m, nil
		}
	}

	var cmd bubbletea.Cmd
	m.project, cmd = m.project.Update(msg)
	return m, cmd
}

func (m browseModel) view() string {
	var b strings.Builder

	// Title line.
	b.WriteString(styleTitle.Render("Wasteland Board"))
	b.WriteByte('\n')

	// Two-line filter bar.
	statusLabel := commons.StatusLabel(commons.ValidStatuses()[m.statusIdx])
	typeLabel := commons.TypeLabel(commons.ValidTypes()[m.typeIdx])

	mineLabel := "OFF"
	mineStr := fmt.Sprintf("[i] Mine: %s", mineLabel)
	if m.myItems {
		mineLabel = "ON"
		mineStr = "[i] Mine: " + styleMineOn.Render(mineLabel)
	}
	sortLabel := commons.SortLabel(commons.ValidSortOrders()[m.sortIdx])

	filterLine1 := fmt.Sprintf("  [s] Status: %-12s  [t] Type: %-10s  %s  [o] Sort: %s",
		statusLabel, typeLabel, mineStr, sortLabel)
	b.WriteString(styleFilterBar.Render(filterLine1))
	b.WriteByte('\n')

	priLabel := commons.PriorityLabel(commons.ValidPriorities()[m.priorityIdx])
	projLabel := "--"
	if m.projectFilter != "" {
		projLabel = m.projectFilter
	}
	filterLine2 := fmt.Sprintf("  [p] Priority: %-8s  [P] Project: %-8s", priLabel, projLabel)
	if m.search.Value() != "" {
		filterLine2 += fmt.Sprintf("  Search: %q", m.search.Value())
	}
	b.WriteString(styleFilterBar.Render(filterLine2))
	b.WriteByte('\n')

	// Text input bars.
	if m.searchMode {
		b.WriteString("  Search: ")
		b.WriteString(m.search.View())
		b.WriteByte('\n')
	}
	if m.projectMode {
		b.WriteString("  Project: ")
		b.WriteString(m.project.View())
		b.WriteByte('\n')
	}

	// Column headers — add POSTED BY and CLAIMED BY for wide terminals.
	wide := m.width > 100
	var colHeader string
	if wide {
		colHeader = fmt.Sprintf("  %-12s %-30s %-10s %-8s %-3s %-10s %-12s %s",
			"ID", "TITLE", "PROJECT", "TYPE", "PRI", "STATUS", "POSTED BY", "CLAIMED BY")
	} else {
		colHeader = fmt.Sprintf("  %-12s %-30s %-10s %-8s %-3s %-10s",
			"ID", "TITLE", "PROJECT", "TYPE", "PRI", "STATUS")
	}
	b.WriteString(styleDim.Render(colHeader))
	b.WriteByte('\n')

	// Separator.
	sep := strings.Repeat("─", min(m.width, lipgloss.Width(colHeader)+2))
	b.WriteString(styleDim.Render(sep))
	b.WriteByte('\n')

	if m.loading {
		b.WriteString(styleDim.Render("  Loading..."))
		return b.String()
	}

	if m.err != nil {
		fmt.Fprintf(&b, "  Error: %v", m.err)
		return b.String()
	}

	if len(m.items) == 0 {
		b.WriteString(styleDim.Render(fmt.Sprintf(
			"  No %s items found (type: %s).", statusLabel, typeLabel)))
		return b.String()
	}

	// Item count.
	b.WriteString(styleDim.Render(fmt.Sprintf("  %d items", len(m.items))))
	b.WriteByte('\n')

	// Compute visible window.
	headerLines := 7 // title + filter1 + filter2 + colheader + sep + count + slack
	if m.searchMode {
		headerLines++
	}
	if m.projectMode {
		headerLines++
	}
	listHeight := m.height - headerLines
	if listHeight < 1 {
		listHeight = 10
	}
	startIdx := 0
	if m.cursor >= listHeight {
		startIdx = m.cursor - listHeight + 1
	}
	endIdx := startIdx + listHeight
	if endIdx > len(m.items) {
		endIdx = len(m.items)
	}

	titleMax := 30
	for i := startIdx; i < endIdx; i++ {
		item := m.items[i]
		title := item.Title
		titleRunes := []rune(title)
		if len(titleRunes) > titleMax {
			title = string(titleRunes[:titleMax-3]) + "..."
		}
		pri := padANSI(colorizePriority(item.Priority), 3)
		status := colorizeStatus(item.Status)
		if m.pendingIDs[item.ID] > 0 {
			status += "*"
		}
		status = padANSI(status, 10)
		claimedBy := item.ClaimedBy
		if wide && claimedBy == "" {
			claimedBy = styleDim.Render("—")
		}
		var line string
		if wide {
			line = fmt.Sprintf("  %-12s %-30s %-10s %-8s %s %s %-12s %s",
				item.ID, title, item.Project, item.Type, pri, status, item.PostedBy, claimedBy)
		} else {
			line = fmt.Sprintf("  %-12s %-30s %-10s %-8s %s %s",
				item.ID, title, item.Project, item.Type, pri, status)
		}

		if i == m.cursor {
			line = styleSelected.Width(m.width).Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return b.String()
}

// padANSI right-pads an ANSI-styled string to width based on visible characters.
func padANSI(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}
