package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	bubbletea "github.com/charmbracelet/bubbletea"
)

// settingsModel holds the state for the Settings view.
type settingsModel struct {
	cursor  int // 0=mode, 1=signing
	mode    string
	signing bool
	width   int
	height  int
	result  string // "Saved" or error text
}

func newSettingsModel(mode string, signing bool) settingsModel {
	return settingsModel{
		mode:    mode,
		signing: signing,
	}
}

func (m *settingsModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

// sync updates the settings model to reflect the current config values.
func (m *settingsModel) sync(mode string, signing bool) {
	m.mode = mode
	m.signing = signing
	m.result = ""
}

func (m settingsModel) update(msg bubbletea.Msg, cfg Config) (settingsModel, bubbletea.Cmd) {
	if msg, ok := msg.(bubbletea.KeyMsg); ok {
		switch {
		case key.Matches(msg, keys.Quit):
			return m, bubbletea.Quit

		case key.Matches(msg, keys.Back):
			return m, func() bubbletea.Msg {
				return navigateMsg{view: viewBrowse}
			}

		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}

		case key.Matches(msg, keys.Down):
			if m.cursor < 1 {
				m.cursor++
			}

		case key.Matches(msg, keys.Enter):
			return m.toggle(cfg)

		case msg.String() == "left" || msg.String() == "right":
			return m.toggle(cfg)
		}
	}
	return m, nil
}

// toggle flips the setting at cursor and returns a save command.
func (m settingsModel) toggle(cfg Config) (settingsModel, bubbletea.Cmd) {
	switch m.cursor {
	case 0: // mode
		if m.mode == "wild-west" {
			m.mode = "pr"
		} else {
			m.mode = "wild-west"
		}
	case 1: // signing
		m.signing = !m.signing
	}

	mode := m.mode
	signing := m.signing
	return m, func() bubbletea.Msg {
		if cfg.Client != nil {
			if err := cfg.Client.SaveSettings(mode, signing); err != nil {
				return settingsSavedMsg{mode: mode, signing: signing, err: err}
			}
		}
		return settingsSavedMsg{mode: mode, signing: signing}
	}
}

func (m settingsModel) view(cfg Config) string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Settings"))
	b.WriteString("\n\n")

	// Read-only context.
	fmt.Fprintf(&b, "  Rig:         %s\n", cfg.RigHandle)
	fmt.Fprintf(&b, "  Upstream:    %s\n", cfg.Upstream)
	fmt.Fprintf(&b, "  Provider:    %s\n", cfg.ProviderType)
	fmt.Fprintf(&b, "  Fork:        %s/%s\n", cfg.ForkOrg, cfg.ForkDB)
	fmt.Fprintf(&b, "  Local:       %s\n", cfg.LocalDir)
	fmt.Fprintf(&b, "  Joined:      %s\n", cfg.JoinedAt)

	b.WriteString("\n")
	b.WriteString(styleDim.Render("  ─── Editable ───────────────────────"))
	b.WriteString("\n\n")

	// Mode toggle.
	modeLine := m.renderToggle("Mode", m.mode, "wild-west", "pr", m.cursor == 0)
	b.WriteString(modeLine)
	b.WriteByte('\n')

	// Signing toggle.
	signingLine := m.renderToggle("Signing", fmt.Sprintf("%t", m.signing), "true", "false", m.cursor == 1)
	b.WriteString(signingLine)
	b.WriteByte('\n')

	// Result feedback.
	if m.result != "" {
		b.WriteString("\n  " + m.result)
		b.WriteByte('\n')
	}

	return b.String()
}

// renderToggle renders a single toggle setting line.
func (m settingsModel) renderToggle(label, current, optA, optB string, active bool) string {
	var a, b string
	if current == optA {
		a = fmt.Sprintf("[%s]", optA)
		b = fmt.Sprintf(" %s", optB)
	} else {
		a = fmt.Sprintf(" %s", optA)
		b = fmt.Sprintf(" [%s]", optB)
	}

	line := fmt.Sprintf("  %-11s %s %s", label+":", a, b)
	if active {
		line = styleSelected.Width(m.width).Render(line)
	}
	return line
}
