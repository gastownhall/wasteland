package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/gastownhall/wasteland/internal/commons"
)

// meModel holds the state for the "My Dashboard" view.
type meModel struct {
	data    *commons.DashboardData
	cursor  int // flat index across all sections
	width   int
	height  int
	loading bool
	err     error
}

func newMeModel() meModel {
	return meModel{loading: true}
}

func (m *meModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *meModel) setData(msg meDataMsg) {
	m.loading = false
	m.err = msg.err
	m.data = msg.data
	total := m.totalItems()
	if m.cursor >= total {
		m.cursor = max(0, total-1)
	}
}

// totalItems returns the number of items across all sections.
func (m meModel) totalItems() int {
	if m.data == nil {
		return 0
	}
	return len(m.data.Claimed) + len(m.data.InReview) + len(m.data.Completed)
}

// selectedItem returns the item at the current cursor position.
func (m meModel) selectedItem() *commons.WantedSummary {
	if m.data == nil {
		return nil
	}
	idx := m.cursor
	if idx < len(m.data.Claimed) {
		return &m.data.Claimed[idx]
	}
	idx -= len(m.data.Claimed)
	if idx < len(m.data.InReview) {
		return &m.data.InReview[idx]
	}
	idx -= len(m.data.InReview)
	if idx < len(m.data.Completed) {
		return &m.data.Completed[idx]
	}
	return nil
}

func (m meModel) update(msg bubbletea.Msg) (meModel, bubbletea.Cmd) {
	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		total := m.totalItems()
		switch {
		case key.Matches(msg, keys.Quit):
			return m, bubbletea.Quit

		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}

		case key.Matches(msg, keys.Down):
			if m.cursor < total-1 {
				m.cursor++
			}

		case key.Matches(msg, keys.Enter):
			if item := m.selectedItem(); item != nil {
				return m, func() bubbletea.Msg {
					return navigateMsg{view: viewDetail, wantedID: item.ID}
				}
			}

		case key.Matches(msg, keys.Back):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewBrowse}
			}

		case key.Matches(msg, keys.Settings):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewSettings}
			}
		}
	}
	return m, nil
}

func (m meModel) view() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("My Dashboard"))
	b.WriteByte('\n')
	b.WriteByte('\n')

	if m.loading {
		b.WriteString(styleDim.Render("  Loading..."))
		return b.String()
	}
	if m.err != nil {
		fmt.Fprintf(&b, "  Error: %v", m.err)
		return b.String()
	}
	if m.data == nil {
		b.WriteString(styleDim.Render("  No data."))
		return b.String()
	}

	flatIdx := 0

	if len(m.data.Claimed) > 0 {
		b.WriteString(styleFilterBar.Render("  My Claimed Items"))
		b.WriteByte('\n')
		for _, item := range m.data.Claimed {
			b.WriteString(m.renderRow(item, flatIdx))
			flatIdx++
		}
		b.WriteByte('\n')
	}

	if len(m.data.InReview) > 0 {
		b.WriteString(styleFilterBar.Render("  Awaiting My Review"))
		b.WriteByte('\n')
		for _, item := range m.data.InReview {
			b.WriteString(m.renderRow(item, flatIdx))
			flatIdx++
		}
		b.WriteByte('\n')
	}

	if len(m.data.Completed) > 0 {
		b.WriteString(styleFilterBar.Render("  Recent Completions"))
		b.WriteByte('\n')
		for _, item := range m.data.Completed {
			b.WriteString(m.renderRow(item, flatIdx))
			flatIdx++
		}
		b.WriteByte('\n')
	}

	if flatIdx == 0 {
		b.WriteString(styleDim.Render("  No items to show."))
		b.WriteByte('\n')
	}

	return b.String()
}

func (m meModel) renderRow(item commons.WantedSummary, flatIdx int) string {
	title := item.Title
	titleRunes := []rune(title)
	if len(titleRunes) > 30 {
		title = string(titleRunes[:27]) + "..."
	}
	pri := colorizePriority(item.Priority)
	status := colorizeStatus(item.Status)
	line := fmt.Sprintf("  %-12s %-30s %-10s %-4s %-10s",
		item.ID, title, status, pri, item.Project)

	if flatIdx == m.cursor {
		line = styleSelected.Width(m.width).Render(line)
	}
	return line + "\n"
}
