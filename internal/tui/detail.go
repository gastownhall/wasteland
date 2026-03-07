package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/gastownhall/wasteland/internal/commons"
)

// confirmAction holds state while waiting for the user to confirm.
type confirmAction struct {
	transition commons.Transition
	label      string
}

// deltaConfirmAction holds state while waiting for the user to confirm a delta action.
type deltaConfirmAction struct {
	action branchDeltaAction
	label  string
}

type detailModel struct {
	item       *commons.WantedItem
	completion *commons.CompletionRecord
	stamp      *commons.Stamp
	viewport   viewport.Model
	width      int
	height     int
	loading    bool
	err        error

	// Mutation state.
	// dbDir was removed; DB is now on Config
	rigHandle      string
	mode           string
	branch         string              // non-empty when showing branch state
	mainStatus     string              // status on main when showing branch state
	prURL          string              // non-empty when upstream PR already exists
	branchActions  []string            // SDK-computed: "submit_pr", "apply", "discard"
	confirming     *confirmAction      // non-nil → showing confirmation prompt
	deltaConfirm   *deltaConfirmAction // non-nil → showing delta confirmation prompt
	executing      bool                // true → showing spinner
	executingLabel string              // e.g. "Claiming..."
	spinner        spinner.Model
	result         string // brief success/error message

	// Sub-state forms.
	submit     *submitModel
	doneForm   *doneFormModel
	acceptForm *acceptFormModel
}

func newDetailModel(rigHandle, mode string) detailModel {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	return detailModel{
		rigHandle: rigHandle,
		mode:      mode,
		spinner:   s,
		loading:   true,
	}
}

func (m *detailModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = h - 2 // room for title + padding
}

func (m *detailModel) refreshViewport() {
	if m.item != nil {
		m.viewport.SetContent(m.renderContent())
	}
}

func (m *detailModel) setData(msg detailDataMsg) {
	m.loading = false
	m.err = msg.err
	m.item = msg.item
	m.completion = msg.completion
	m.stamp = msg.stamp
	m.branch = msg.branch
	m.mainStatus = msg.mainStatus
	m.prURL = msg.prURL
	m.branchActions = msg.branchActions
	// Clear mutation state so stale results don't mask action hints.
	m.confirming = nil
	m.deltaConfirm = nil
	m.executing = false
	m.executingLabel = ""
	m.result = ""
	m.submit = nil
	m.doneForm = nil
	m.acceptForm = nil
	if m.item != nil {
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoTop()
	}
}

func (m detailModel) update(msg bubbletea.Msg) (detailModel, bubbletea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.executing {
			var cmd bubbletea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case bubbletea.KeyMsg:
		// While executing, only allow ctrl+c.
		if m.executing {
			return m, nil
		}

		// Confirmation prompt active: handle y/n/esc only.
		if m.confirming != nil {
			switch {
			case key.Matches(msg, keys.Confirm):
				t := m.confirming.transition
				m.confirming = nil
				return m, func() bubbletea.Msg {
					return actionConfirmedMsg{transition: t}
				}
			case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Back):
				m.confirming = nil
				return m, nil
			}
			return m, nil
		}

		// Delta confirmation prompt active: handle y/n/esc only.
		if m.deltaConfirm != nil {
			switch {
			case key.Matches(msg, keys.Confirm):
				a := m.deltaConfirm.action
				m.deltaConfirm = nil
				return m, func() bubbletea.Msg {
					return deltaConfirmedMsg{action: a}
				}
			case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Back):
				m.deltaConfirm = nil
				return m, nil
			}
			return m, nil
		}

		// Submit view active: route to submit model.
		if m.submit != nil {
			var cmd bubbletea.Cmd
			m.submit, cmd = m.submit.update(msg)
			if m.submit == nil {
				// Canceled — restore viewport content.
				m.refreshViewport()
			}
			return m, cmd
		}

		// Done form active: route to done form.
		if m.doneForm != nil {
			var cmd bubbletea.Cmd
			m.doneForm, cmd = m.doneForm.update(msg)
			m.refreshViewport()
			return m, cmd
		}

		// Accept form active: route to accept form.
		if m.acceptForm != nil {
			var cmd bubbletea.Cmd
			m.acceptForm, cmd = m.acceptForm.update(msg)
			m.refreshViewport()
			return m, cmd
		}

		// Normal key handling.
		switch {
		case key.Matches(msg, keys.Back):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewBrowse}
			}
		case key.Matches(msg, keys.Quit):
			return m, bubbletea.Quit

		// Done/accept inline forms.
		case key.Matches(msg, keys.Done):
			return m.tryDoneForm()
		case key.Matches(msg, keys.Accept):
			return m.tryAcceptForm()

		// Executable actions.
		case key.Matches(msg, keys.Claim):
			return m.tryAction(commons.TransitionClaim)
		case key.Matches(msg, keys.Unclaim):
			return m.tryAction(commons.TransitionUnclaim)
		case key.Matches(msg, keys.Reject):
			return m.tryAction(commons.TransitionReject)
		case key.Matches(msg, keys.Close):
			return m.tryAction(commons.TransitionClose)
		case key.Matches(msg, keys.Delete):
			return m.tryAction(commons.TransitionDelete)

		// Delta resolution keys.
		case key.Matches(msg, keys.Apply):
			return m.tryDelta(deltaApply)
		case key.Matches(msg, keys.Discard):
			return m.tryDelta(deltaDiscard)
		}
	}

	var cmd bubbletea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// tryAction validates a transition and permission, then returns an actionRequestMsg.
func (m detailModel) tryAction(t commons.Transition) (detailModel, bubbletea.Cmd) {
	if m.item == nil {
		return m, nil
	}
	if _, err := commons.ValidateTransition(m.item.Status, t); err != nil {
		m.result = styleError.Render(err.Error())
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	if !commons.CanPerformTransition(m.item, t, m.rigHandle) {
		name := commons.TransitionName(t)
		m.result = styleError.Render(fmt.Sprintf("cannot %s: permission denied", name))
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	name := commons.TransitionName(t)
	verb := strings.ToUpper(name[:1]) + name[1:]
	label := fmt.Sprintf("%s %s?", verb, m.item.ID)
	return m, func() bubbletea.Msg {
		return actionRequestMsg{transition: t, label: label}
	}
}

// tryDoneForm validates the done transition and opens the evidence input form.
func (m detailModel) tryDoneForm() (detailModel, bubbletea.Cmd) {
	if m.item == nil {
		return m, nil
	}
	if _, err := commons.ValidateTransition(m.item.Status, commons.TransitionDone); err != nil {
		m.result = styleError.Render(err.Error())
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	if !commons.CanPerformTransition(m.item, commons.TransitionDone, m.rigHandle) {
		m.result = styleError.Render("cannot done: permission denied")
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	m.result = ""
	m.doneForm = newDoneForm()
	m.viewport.SetContent(m.renderContent())
	return m, nil
}

// tryAcceptForm validates the accept transition and opens the stamp form.
func (m detailModel) tryAcceptForm() (detailModel, bubbletea.Cmd) {
	if m.item == nil {
		return m, nil
	}
	if _, err := commons.ValidateTransition(m.item.Status, commons.TransitionAccept); err != nil {
		m.result = styleError.Render(err.Error())
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	if !commons.CanPerformTransition(m.item, commons.TransitionAccept, m.rigHandle) {
		m.result = styleError.Render("cannot accept: permission denied")
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	m.result = ""
	m.acceptForm = newAcceptForm()
	m.viewport.SetContent(m.renderContent())
	return m, nil
}

// submitOpenedMsg signals to the root that the submit view was opened
// and diff loading should begin.
type submitOpenedMsg struct {
	branch string
}

// trySubmit opens the submit PR view (M key in PR mode with a branch).
func (m detailModel) trySubmit() (detailModel, bubbletea.Cmd) {
	if m.branch == "" || m.item == nil {
		return m, nil
	}
	// PR already submitted — show the URL instead.
	if m.prURL != "" {
		m.result = styleDim.Render(fmt.Sprintf("PR already open: %s", m.prURL))
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}
	m.result = ""
	m.submit = newSubmitModel(m.item, m.branch, m.mainStatus, m.width, m.height-2)
	branch := m.branch
	return m, func() bubbletea.Msg {
		return submitOpenedMsg{branch: branch}
	}
}

// tryDelta validates that a branch exists, computes a label, and returns a deltaRequestMsg.
func (m detailModel) tryDelta(action branchDeltaAction) (detailModel, bubbletea.Cmd) {
	if m.branch == "" || m.item == nil {
		return m, nil
	}
	// PR mode: M opens the submit PR view instead of applying locally.
	if action == deltaApply && m.mode == "pr" {
		return m.trySubmit()
	}
	delta := commons.DeltaLabel(m.mainStatus, m.item.Status)
	var label string
	switch action {
	case deltaApply:
		label = fmt.Sprintf("Apply %s to main? Pushes to origin. [y/n]", delta)
	case deltaDiscard:
		label = fmt.Sprintf("Discard %s? Reverts to %s. Deletes local + remote branch. [y/n]", delta, m.mainStatus)
	}
	return m, func() bubbletea.Msg {
		return deltaRequestMsg{action: action, label: label}
	}
}

func (m detailModel) view() string {
	if m.loading {
		return styleDim.Render("  Loading...")
	}
	if m.err != nil {
		return fmt.Sprintf("  Error: %v", m.err)
	}
	if m.item == nil {
		return styleDim.Render("  No item loaded.")
	}

	// Submit view replaces the entire viewport content.
	if m.submit != nil {
		return m.submit.view()
	}

	title := styleTitle.Render(fmt.Sprintf("%s: %s", m.item.ID, m.item.Title))
	return title + "\n" + m.viewport.View()
}

func (m detailModel) renderContent() string {
	item := m.item
	var b strings.Builder

	fmt.Fprintf(&b, "\n  Status:      %s\n", colorizeStatus(item.Status))
	if m.branch != "" && m.mainStatus != "" && m.mainStatus != item.Status {
		fmt.Fprintf(&b, "  Pending:     %s → %s\n", m.mainStatus, item.Status)
	}
	if m.branch != "" {
		b.WriteString(styleDim.Render(fmt.Sprintf("  Branch:      %s", m.branch)) + "\n")
	}
	if m.prURL != "" {
		b.WriteString(styleSuccess.Render(fmt.Sprintf("  PR:          %s", m.prURL)) + "\n")
	}

	if item.Type != "" {
		fmt.Fprintf(&b, "  Type:        %-14s", item.Type)
	} else {
		fmt.Fprintf(&b, "  %-28s", "")
	}
	fmt.Fprintf(&b, "Priority: %s\n", colorizePriority(item.Priority))

	if item.Project != "" {
		fmt.Fprintf(&b, "  Project:     %-14s", item.Project)
	} else {
		fmt.Fprintf(&b, "  %-28s", "")
	}
	fmt.Fprintf(&b, "Effort:   %s\n", item.EffortLevel)

	if item.PostedBy != "" {
		fmt.Fprintf(&b, "  Posted by:   %s\n", item.PostedBy)
	}

	if item.ClaimedBy != "" {
		fmt.Fprintf(&b, "  Claimed by:  %s\n", item.ClaimedBy)
	}

	if len(item.Tags) > 0 {
		fmt.Fprintf(&b, "  Tags:        %s\n", strings.Join(item.Tags, ", "))
	}

	if item.CreatedAt != "" {
		fmt.Fprintf(&b, "  Created:     %s\n", item.CreatedAt)
	}
	if item.UpdatedAt != "" {
		fmt.Fprintf(&b, "  Updated:     %s\n", item.UpdatedAt)
	}

	if item.Description != "" {
		b.WriteString("\n  Description:\n")
		fmt.Fprintf(&b, "    %s\n", item.Description)
	}

	if m.completion != nil {
		fmt.Fprintf(&b, "\n  Completion:  %s\n", m.completion.ID)
		if m.completion.Evidence != "" {
			fmt.Fprintf(&b, "    Evidence:    %s\n", m.completion.Evidence)
		}
		fmt.Fprintf(&b, "    Completed by: %s\n", m.completion.CompletedBy)
	}

	if m.stamp != nil {
		fmt.Fprintf(&b, "\n  Stamp:       %s\n", m.stamp.ID)
		fmt.Fprintf(&b, "    Quality: %d  Reliability: %d  Severity: %s\n",
			m.stamp.Quality, m.stamp.Reliability, m.stamp.Severity)
		if len(m.stamp.SkillTags) > 0 {
			fmt.Fprintf(&b, "    Skills:      %s\n", strings.Join(m.stamp.SkillTags, ", "))
		}
		if m.stamp.Author != "" {
			fmt.Fprintf(&b, "    Accepted by: %s\n", m.stamp.Author)
		}
		if m.stamp.Message != "" {
			fmt.Fprintf(&b, "    Message:     %s\n", m.stamp.Message)
		}
	}

	// Status line: confirmation, executing, result, forms, or action hints.
	b.WriteByte('\n')
	switch {
	case m.doneForm != nil:
		b.WriteString(m.doneForm.view())
		return b.String()
	case m.acceptForm != nil:
		b.WriteString(m.acceptForm.view())
		return b.String()
	case m.confirming != nil:
		b.WriteString(styleConfirm.Render(fmt.Sprintf(
			"  %s Pushes to upstream. [y/n]", m.confirming.label)))
	case m.deltaConfirm != nil:
		b.WriteString(styleConfirm.Render(fmt.Sprintf("  %s", m.deltaConfirm.label)))
	case m.executing:
		fmt.Fprintf(&b, "  %s %s", m.spinner.View(), m.executingLabel)
	case m.result != "":
		b.WriteString("  " + m.result)
		b.WriteByte('\n')
		b.WriteString(styleDim.Render(m.actionHints()))
	default:
		b.WriteString(styleDim.Render(m.actionHints()))
	}
	b.WriteByte('\n')

	return b.String()
}

// transitionKeyHint maps transitions to their TUI key bindings.
var transitionKeyHint = map[commons.Transition]string{
	commons.TransitionClaim:   "c",
	commons.TransitionUnclaim: "u",
	commons.TransitionDone:    "d",
	commons.TransitionAccept:  "a",
	commons.TransitionReject:  "x",
	commons.TransitionClose:   "X",
	commons.TransitionDelete:  "D",
}

// actionHints returns a string showing valid lifecycle actions for the item,
// filtered by both status validity and permission.
func (m detailModel) actionHints() string {
	if m.item == nil {
		return ""
	}
	available := commons.AvailableTransitions(m.item, m.rigHandle)
	var hints []string
	for _, t := range available {
		k := transitionKeyHint[t]
		name := commons.TransitionName(t)
		hint := k + ":" + name
		if commons.TransitionRequiresInput(t) != "" {
			hint += "*"
		}
		hints = append(hints, hint)
	}

	// Branch actions from SDK-computed list.
	if len(m.branchActions) > 0 {
		delta := commons.DeltaLabel(m.mainStatus, m.item.Status)
		var deltaHints []string
		for _, action := range m.branchActions {
			switch action {
			case "submit_pr":
				deltaHints = append(deltaHints, fmt.Sprintf("M:submit PR (%s)", delta))
			case "apply":
				deltaHints = append(deltaHints, fmt.Sprintf("M:apply %s", delta))
			case "discard":
				deltaHints = append(deltaHints, fmt.Sprintf("b:discard (→ %s)", m.mainStatus))
			}
		}
		if len(hints) > 0 {
			hints = append(hints, "|")
		}
		hints = append(hints, deltaHints...)
	}

	if len(hints) == 0 {
		return "  (no actions available)"
	}
	return "  Actions: " + strings.Join(hints, "  ")
}
