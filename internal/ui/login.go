package ui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// loginField indexes the form inputs.
type loginField int

const (
	fieldServer loginField = iota
	fieldUser
	fieldPassword
	fieldTOTP
	fieldToken
	fieldCount
)

// loginResultMsg reports the outcome of a login attempt.
type loginResultMsg struct {
	session app.Session
	err     error
}

// loginModel is the sign-in form. It owns the text inputs; render.Login owns the
// layout.
type loginModel struct {
	theme  render.Theme
	inputs [fieldCount]textinput.Model

	focus    loginField
	usePAT   bool
	needTOTP bool
	busy     bool
	errText  string
	message  string

	width  int
	height int
}

func newLoginModel(theme render.Theme, serverURL, username string) loginModel {
	m := loginModel{theme: theme, focus: fieldServer}

	newInput := func(placeholder, value string, width int) textinput.Model {
		input := textinput.New()
		input.Placeholder = placeholder
		input.SetValue(value)
		input.Prompt = ""
		input.CharLimit = 512
		input.Width = width
		return input
	}

	m.inputs[fieldServer] = newInput("chat.example.com", serverURL, 30)
	m.inputs[fieldUser] = newInput("username or email", username, 30)
	m.inputs[fieldPassword] = newInput("password", "", 30)
	m.inputs[fieldPassword].EchoMode = textinput.EchoPassword
	m.inputs[fieldPassword].EchoCharacter = '•'
	m.inputs[fieldTOTP] = newInput("123456", "", 10)
	m.inputs[fieldToken] = newInput("personal access token", "", 30)
	m.inputs[fieldToken].EchoMode = textinput.EchoPassword
	m.inputs[fieldToken].EchoCharacter = '•'

	// Start on the first empty field so a remembered server is skipped.
	if serverURL != "" {
		m.focus = fieldUser
	}
	m.syncFocus()
	return m
}

// visibleFields lists the fields for the current mode, in tab order.
func (m loginModel) visibleFields() []loginField {
	if m.usePAT {
		return []loginField{fieldServer, fieldToken}
	}
	fields := []loginField{fieldServer, fieldUser, fieldPassword}
	if m.needTOTP {
		fields = append(fields, fieldTOTP)
	}
	return fields
}

func (m *loginModel) syncFocus() {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
	m.inputs[m.focus].Focus()
}

// advance moves focus by delta within the visible fields, wrapping around.
func (m *loginModel) advance(delta int) {
	fields := m.visibleFields()
	current := 0
	for i, field := range fields {
		if field == m.focus {
			current = i
			break
		}
	}
	next := (current + delta + len(fields)) % len(fields)
	m.focus = fields[next]
	m.syncFocus()
}

func (m loginModel) Init() tea.Cmd { return textinput.Blink }

func (m loginModel) Update(msg tea.Msg) (loginModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case loginResultMsg:
		m.busy = false
		if msg.err == nil {
			return m, nil // the root model handles success
		}
		if app.IsTOTPRequired(msg.err) {
			m.needTOTP = true
			m.errText = ""
			m.message = "This account uses two-factor authentication."
			m.focus = fieldTOTP
			m.syncFocus()
			return m, nil
		}
		m.errText = app.LoginErrorText(msg.err)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "tab", "down":
			m.advance(1)
			return m, nil

		case "shift+tab", "up":
			m.advance(-1)
			return m, nil

		case "ctrl+t":
			m.usePAT = !m.usePAT
			m.needTOTP = false
			m.errText = ""
			m.message = ""
			if m.usePAT {
				m.focus = fieldToken
			} else {
				m.focus = fieldUser
			}
			m.syncFocus()
			return m, nil

		case "enter":
			if m.busy {
				return m, nil
			}
			// Enter on a non-final field just advances, matching form habits.
			fields := m.visibleFields()
			if m.focus != fields[len(fields)-1] && m.currentValue() != "" {
				m.advance(1)
				return m, nil
			}
			return m.submit()
		}
	}

	if m.busy {
		return m, nil
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m loginModel) currentValue() string {
	return strings.TrimSpace(m.inputs[m.focus].Value())
}

func (m loginModel) submit() (loginModel, tea.Cmd) {
	params := app.LoginParams{
		ServerURL: strings.TrimSpace(m.inputs[fieldServer].Value()),
		Username:  strings.TrimSpace(m.inputs[fieldUser].Value()),
		Password:  m.inputs[fieldPassword].Value(),
		TOTP:      strings.TrimSpace(m.inputs[fieldTOTP].Value()),
	}
	if m.usePAT {
		params.Token = strings.TrimSpace(m.inputs[fieldToken].Value())
		params.Username, params.Password, params.TOTP = "", "", ""
	}

	switch {
	case params.ServerURL == "":
		m.errText = "Enter your Rocket.Chat server address."
		m.focus = fieldServer
		m.syncFocus()
		return m, nil
	case m.usePAT && params.Token == "":
		m.errText = "Enter a personal access token."
		return m, nil
	case !m.usePAT && (params.Username == "" || params.Password == ""):
		m.errText = "Enter your username and password."
		return m, nil
	}

	m.busy = true
	m.errText = ""
	m.message = ""
	return m, doLogin(params)
}

// doLogin performs the network call off the UI goroutine.
func doLogin(params app.LoginParams) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		session, err := app.Login(ctx, params)
		return loginResultMsg{session: session, err: err}
	}
}

func (m loginModel) View() string {
	state := render.LoginState{
		Width:   m.width,
		Height:  m.height,
		UsePAT:  m.usePAT,
		Error:   m.errText,
		Busy:    m.busy,
		Message: m.message,
	}

	labels := map[loginField]string{
		fieldServer:   "Server",
		fieldUser:     "Username",
		fieldPassword: "Password",
		fieldTOTP:     "2FA code",
		fieldToken:    "Token",
	}
	hints := map[loginField]string{
		fieldServer: "https:// is assumed when omitted",
		fieldToken:  "My Account → Personal Access Tokens",
	}

	for _, field := range m.visibleFields() {
		state.Fields = append(state.Fields, render.LoginField{
			Label:   labels[field],
			View:    m.inputs[field].View(),
			Focused: field == m.focus,
			Hint:    hints[field],
		})
	}
	return render.Login(m.theme, state)
}
