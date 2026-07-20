package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dwalters/tkn-tstart/internal/run"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	labelStyle    = lipgloss.NewStyle().Width(30).Foreground(lipgloss.Color("7"))
	requiredMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" *")
	descStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type Result struct {
	Submitted bool
	Params    []*run.Param
}

type model struct {
	params    []*run.Param
	inputs    []textinput.Model
	enumIdx   []int
	cursor    int
	submitted bool
	errorMsg  string
	kind      string
	name      string
}

func newModel(manifest *run.Manifest) model {
	inputs := make([]textinput.Model, len(manifest.Params))
	enumIdx := make([]int, len(manifest.Params))

	for i, p := range manifest.Params {
		if len(p.Enum) > 0 {
			for j, e := range p.Enum {
				if e == p.Value {
					enumIdx[i] = j
					break
				}
			}
		} else {
			ti := textinput.New()
			ti.SetValue(p.Value)
			ti.Placeholder = p.Description
			ti.CharLimit = 512
			inputs[i] = ti
		}
	}

	m := model{
		params:  manifest.Params,
		inputs:  inputs,
		enumIdx: enumIdx,
		kind:    manifest.Kind,
	}
	if meta, ok := manifest.Raw["metadata"].(map[string]interface{}); ok {
		m.name, _ = meta["name"].(string)
		if m.name == "" {
			if gn, ok := meta["generateName"].(string); ok {
				m.name = gn + "(generated)"
			}
		}
	}
	m.focusCurrent()
	return m
}

func (m *model) focusCurrent() {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	if m.cursor < len(m.inputs) && len(m.params[m.cursor].Enum) == 0 {
		m.inputs[m.cursor].Focus()
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "tab", "down":
			m.syncCurrent()
			m.cursor = (m.cursor + 1) % len(m.params)
			m.focusCurrent()
			return m, nil

		case "shift+tab", "up":
			m.syncCurrent()
			m.cursor = (m.cursor - 1 + len(m.params)) % len(m.params)
			m.focusCurrent()
			return m, nil

		case "left":
			if len(m.params[m.cursor].Enum) > 0 {
				n := len(m.params[m.cursor].Enum)
				m.enumIdx[m.cursor] = (m.enumIdx[m.cursor] - 1 + n) % n
			}
			return m, nil

		case "right":
			if len(m.params[m.cursor].Enum) > 0 {
				n := len(m.params[m.cursor].Enum)
				m.enumIdx[m.cursor] = (m.enumIdx[m.cursor] + 1) % n
			}
			return m, nil

		case "enter", "ctrl+s":
			m.syncCurrent()
			if errMsg := m.validate(); errMsg != "" {
				m.errorMsg = errMsg
				return m, nil
			}
			m.submitted = true
			return m, tea.Quit
		}
	}

	if m.cursor < len(m.inputs) && len(m.params[m.cursor].Enum) == 0 {
		var cmd tea.Cmd
		m.inputs[m.cursor], cmd = m.inputs[m.cursor].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) syncCurrent() {
	i := m.cursor
	if len(m.params[i].Enum) > 0 {
		m.params[i].Value = m.params[i].Enum[m.enumIdx[i]]
	} else {
		m.params[i].Value = m.inputs[i].Value()
	}
}

func (m model) validate() string {
	for i, p := range m.params {
		val := m.inputs[i].Value()
		if len(p.Enum) > 0 {
			val = p.Enum[m.enumIdx[i]]
		}
		p.Value = val
		if p.IsRequired() {
			return fmt.Sprintf("parameter %q requires a value", p.Name)
		}
	}
	return ""
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(fmt.Sprintf("%s  %s", m.kind, m.name)) + "\n\n")

	for i, p := range m.params {
		focused := i == m.cursor

		label := p.Name
		if p.IsRequired() {
			label += requiredMark
		}
		b.WriteString(labelStyle.Render(label))

		if len(p.Enum) > 0 {
			b.WriteString(renderEnum(p.Enum, m.enumIdx[i], focused))
		} else {
			if focused {
				b.WriteString(m.inputs[i].View())
			} else {
				val := m.inputs[i].Value()
				if val == "" {
					b.WriteString(descStyle.Render("(empty)"))
				} else {
					b.WriteString(val)
				}
			}
		}

		if p.Description != "" {
			b.WriteString("\n" + strings.Repeat(" ", 30) + descStyle.Render(p.Description))
		}
		b.WriteString("\n")
	}

	if m.errorMsg != "" {
		b.WriteString("\n" + errorStyle.Render("Error: "+m.errorMsg) + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("tab/↑↓ navigate  ←/→ select enum  enter/ctrl+s start  esc cancel"))

	return b.String()
}

func renderEnum(options []string, idx int, focused bool) string {
	var parts []string
	for i, opt := range options {
		if i == idx {
			if focused {
				parts = append(parts, cursorStyle.Render("[ "+opt+" ]"))
			} else {
				parts = append(parts, selectedStyle.Render("[ "+opt+" ]"))
			}
		} else {
			parts = append(parts, "  "+opt+"  ")
		}
	}
	return strings.Join(parts, " ")
}

// Run launches the TUI and returns the result.
func Run(manifest *run.Manifest) (*Result, error) {
	m := newModel(manifest)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	fm := final.(model)

	for i, p := range fm.params {
		if len(p.Enum) > 0 {
			p.Value = p.Enum[fm.enumIdx[i]]
		} else {
			p.Value = fm.inputs[i].Value()
		}
	}

	return &Result{
		Submitted: fm.submitted,
		Params:    fm.params,
	}, nil
}
