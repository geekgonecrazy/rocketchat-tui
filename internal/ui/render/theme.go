// Package render turns view state into strings.
//
// Nothing here knows about Bubbletea, input handling, or the event loop: every
// exported function is pure, so a view can be rendered (and diffed in a test)
// without starting a program.
package render

import "github.com/charmbracelet/lipgloss"

// Palette is the raw colour set; Theme wraps it into ready-to-use styles.
type Palette struct {
	Text      lipgloss.TerminalColor
	Muted     lipgloss.TerminalColor
	Faint     lipgloss.TerminalColor
	Accent    lipgloss.TerminalColor
	Unread    lipgloss.TerminalColor
	Mention   lipgloss.TerminalColor
	Danger    lipgloss.TerminalColor
	Success   lipgloss.TerminalColor
	Border    lipgloss.TerminalColor
	Selection lipgloss.TerminalColor
	Own       lipgloss.TerminalColor
	System    lipgloss.TerminalColor
}

// DefaultPalette works on both light and dark terminals via adaptive colours.
func DefaultPalette() Palette {
	return Palette{
		Text:      lipgloss.AdaptiveColor{Light: "#1F2329", Dark: "#E5E9F0"},
		Muted:     lipgloss.AdaptiveColor{Light: "#5A6472", Dark: "#9AA5B1"},
		Faint:     lipgloss.AdaptiveColor{Light: "#8C97A5", Dark: "#6B7684"},
		Accent:    lipgloss.AdaptiveColor{Light: "#0B65C2", Dark: "#6CB6FF"},
		Unread:    lipgloss.AdaptiveColor{Light: "#1F2329", Dark: "#FFFFFF"},
		Mention:   lipgloss.AdaptiveColor{Light: "#C2185B", Dark: "#FF7A93"},
		Danger:    lipgloss.AdaptiveColor{Light: "#C4314B", Dark: "#FF6B7F"},
		Success:   lipgloss.AdaptiveColor{Light: "#1E7B4D", Dark: "#7EE2A8"},
		Border:    lipgloss.AdaptiveColor{Light: "#D0D7DE", Dark: "#3A4250"},
		Selection: lipgloss.AdaptiveColor{Light: "#DCE7F5", Dark: "#2A3444"},
		Own:       lipgloss.AdaptiveColor{Light: "#7A4CBF", Dark: "#C6A6FF"},
		System:    lipgloss.AdaptiveColor{Light: "#8C97A5", Dark: "#7A8694"},
	}
}

// Theme holds every style the renderers use. Built once and passed by value.
type Theme struct {
	Palette Palette

	Text  lipgloss.Style
	Muted lipgloss.Style
	Faint lipgloss.Style

	Header      lipgloss.Style
	HeaderTitle lipgloss.Style
	HeaderMeta  lipgloss.Style

	SidebarTitle    lipgloss.Style
	SidebarRoom     lipgloss.Style
	SidebarUnread   lipgloss.Style
	SidebarSelected lipgloss.Style
	SidebarCursor   lipgloss.Style
	Badge           lipgloss.Style
	MentionBadge    lipgloss.Style

	Author       lipgloss.Style
	OwnAuthor    lipgloss.Style
	Time         lipgloss.Style
	Body         lipgloss.Style
	SystemMsg    lipgloss.Style
	ThreadHint   lipgloss.Style
	Reaction     lipgloss.Style
	ReactionMine lipgloss.Style
	Attachment   lipgloss.Style
	// Quote is someone else's words, quoted: dimmer than the reply they belong
	// to, so the eye lands on what is being said now rather than on what it
	// answers.
	Quote        lipgloss.Style
	Divider      lipgloss.Style
	UnreadRule   lipgloss.Style
	DateRule     lipgloss.Style
	SelectedBar  lipgloss.Style
	Typing       lipgloss.Style
	Status       lipgloss.Style
	StatusErr    lipgloss.Style
	StatusOK     lipgloss.Style
	Key          lipgloss.Style
	Border       lipgloss.Style
	PromptLabel  lipgloss.Style
	PromptActive lipgloss.Style
	Title        lipgloss.Style
}

// NewTheme builds the style set from a palette.
func NewTheme(p Palette) Theme {
	base := lipgloss.NewStyle()
	return Theme{
		Palette: p,

		Text:  base.Foreground(p.Text),
		Muted: base.Foreground(p.Muted),
		Faint: base.Foreground(p.Faint),

		Header:      base.Foreground(p.Text).Bold(true),
		HeaderTitle: base.Foreground(p.Text).Bold(true),
		HeaderMeta:  base.Foreground(p.Muted),

		SidebarTitle:    base.Foreground(p.Faint).Bold(true),
		SidebarRoom:     base.Foreground(p.Muted),
		SidebarUnread:   base.Foreground(p.Unread).Bold(true),
		SidebarSelected: base.Foreground(p.Text).Background(p.Selection),
		SidebarCursor:   base.Foreground(p.Accent),
		Badge:           base.Foreground(p.Accent).Bold(true),
		MentionBadge:    base.Foreground(p.Mention).Bold(true),

		Author:       base.Foreground(p.Accent).Bold(true),
		OwnAuthor:    base.Foreground(p.Own).Bold(true),
		Time:         base.Foreground(p.Faint),
		Body:         base.Foreground(p.Text),
		SystemMsg:    base.Foreground(p.System).Italic(true),
		ThreadHint:   base.Foreground(p.Accent),
		Reaction:     base.Foreground(p.Muted),
		ReactionMine: base.Foreground(p.Accent).Bold(true),
		Attachment:   base.Foreground(p.Muted).Italic(true),
		Quote:        base.Foreground(p.Faint).Italic(true),
		Divider:      base.Foreground(p.Border),
		UnreadRule:   base.Foreground(p.Danger).Bold(true),
		DateRule:     base.Foreground(p.Faint),
		SelectedBar:  base.Foreground(p.Accent).Bold(true),
		Typing:       base.Foreground(p.Success).Italic(true),
		Status:       base.Foreground(p.Muted),
		StatusErr:    base.Foreground(p.Danger),
		StatusOK:     base.Foreground(p.Success),
		Key:          base.Foreground(p.Accent).Bold(true),
		Border:       base.Foreground(p.Border),

		PromptLabel:  base.Foreground(p.Muted),
		PromptActive: base.Foreground(p.Accent).Bold(true),
		Title:        base.Foreground(p.Text).Bold(true),
	}
}

// DefaultTheme is the theme used unless configured otherwise.
func DefaultTheme() Theme { return NewTheme(DefaultPalette()) }
