package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/gastownhall/wasteland/internal/commons"
)

type submitModel struct {
	item       *commons.WantedItem
	branch     string
	mainStatus string
	diff       string
	diffLoaded bool
	diffErr    error
	showDiff   bool
	viewport   viewport.Model
	width      int
	height     int
}

func newSubmitModel(item *commons.WantedItem, branch, mainStatus string, w, h int) *submitModel {
	vp := viewport.New(w, h-2)
	sm := &submitModel{
		item:       item,
		branch:     branch,
		mainStatus: mainStatus,
		width:      w,
		height:     h,
		viewport:   vp,
	}
	sm.refreshContent()
	return sm
}

func (m *submitModel) setDiff(msg submitDiffMsg) {
	m.diffLoaded = true
	m.diff = msg.diff
	m.diffErr = msg.err
	m.refreshContent()
}

func (m *submitModel) refreshContent() {
	m.viewport.SetContent(m.renderContent())
}

func (m *submitModel) renderContent() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(styleTitle.Render("  Submit PR") + "\n\n")
	fmt.Fprintf(&b, "  %s: %s\n", m.item.ID, m.item.Title)

	delta := commons.DeltaLabel(m.mainStatus, m.item.Status)
	fmt.Fprintf(&b, "  Transition:  %s → %s (%s)\n", m.mainStatus, m.item.Status, delta)
	fmt.Fprintf(&b, "  Branch:      %s\n", m.branch)
	b.WriteString("\n")

	if m.showDiff {
		b.WriteString(styleTitle.Render("  ─── Diff ───") + "\n\n")
		switch {
		case !m.diffLoaded:
			b.WriteString(styleDim.Render("  Loading diff...") + "\n")
		case m.diffErr != nil:
			b.WriteString(styleError.Render(fmt.Sprintf("  Diff error: %v", m.diffErr)) + "\n")
		case m.diff == "":
			b.WriteString(styleDim.Render("  (no changes)") + "\n")
		default:
			b.WriteString(m.diff + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString(styleDim.Render("  (press tab to show diff)") + "\n\n")
	}

	b.WriteString(styleDim.Render("  enter: create PR   esc: back   tab: diff   q: quit") + "\n")

	return b.String()
}

func (m *submitModel) update(msg bubbletea.Msg) (*submitModel, bubbletea.Cmd) {
	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		switch msg.String() {
		case "esc":
			return nil, nil
		case "q", "ctrl+c":
			return m, bubbletea.Quit
		case "tab":
			m.showDiff = !m.showDiff
			m.refreshContent()
			return m, nil
		case "enter":
			// Signal PR creation — root handles the actual execution.
			return m, func() bubbletea.Msg {
				return submitConfirmMsg{}
			}
		}
	}

	var cmd bubbletea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *submitModel) view() string {
	return m.viewport.View()
}

// submitConfirmMsg is an internal message to trigger PR creation from the submit view.
type submitConfirmMsg struct{}
