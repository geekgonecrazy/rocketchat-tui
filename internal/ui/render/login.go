package render

import "strings"

// LoginField is one row of the login form. View is the pre-rendered input
// widget: the model owns the widget, this package only frames it.
type LoginField struct {
	Label   string
	View    string
	Focused bool
	Hint    string
}

// LoginState is the input for the login screen.
type LoginState struct {
	Width   int
	Height  int
	Fields  []LoginField
	UsePAT  bool
	Error   string
	Busy    bool
	Message string
}

// Login renders the login form centred on the screen.
func Login(theme Theme, state LoginState) string {
	const formWidth = 52

	labelWidth := 0
	for _, field := range state.Fields {
		labelWidth = max(labelWidth, Width(field.Label))
	}

	lines := []string{
		theme.Title.Render("Rocket.Chat"),
		theme.Faint.Render("terminal client"),
		"",
	}

	for _, field := range state.Fields {
		marker := "  "
		labelStyle := theme.PromptLabel
		if field.Focused {
			marker = theme.PromptActive.Render("▌ ")
			labelStyle = theme.PromptActive
		}
		row := marker + labelStyle.Render(Pad(field.Label, labelWidth)) + "  " + field.View
		lines = append(lines, row)
		if field.Hint != "" {
			lines = append(lines, strings.Repeat(" ", labelWidth+4)+theme.Faint.Render(field.Hint))
		}
	}

	lines = append(lines, "")
	switch {
	case state.Busy:
		lines = append(lines, theme.Status.Render("  signing in…"))
	case state.Error != "":
		for _, line := range Wrap(state.Error, formWidth) {
			lines = append(lines, theme.StatusErr.Render("  "+line))
		}
	case state.Message != "":
		for _, line := range Wrap(state.Message, formWidth) {
			lines = append(lines, theme.Status.Render("  "+line))
		}
	default:
		lines = append(lines, "")
	}

	mode := "password"
	if state.UsePAT {
		mode = "personal access token"
	}
	lines = append(lines,
		"",
		theme.Faint.Render("  "+
			keyHint(theme, "tab")+" next field   "+
			keyHint(theme, "enter")+" sign in   "+
			keyHint(theme, "ctrl+t")+" "+mode+"   "+
			keyHint(theme, "ctrl+c")+" quit"),
	)

	return Centered(lines, state.Width, state.Height)
}

func keyHint(theme Theme, key string) string {
	return theme.Key.Render(key)
}
