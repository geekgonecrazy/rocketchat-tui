package render

import "strings"

// Frame is a fully-sized chat screen: every field is already the exact number of
// lines and cells it occupies, so assembly is pure concatenation.
type Frame struct {
	Width  int
	Height int

	Header   []string
	Sidebar  []string // SidebarWidth cells wide
	Body     []string // already scrolled to the visible window
	Picker   []string // emoji list, empty when closed
	Typing   string
	Composer []string
	Status   string

	SidebarWidth int
}

// Chat assembles the frame into the final screen string.
func Chat(theme Theme, frame Frame) string {
	if frame.Width <= 0 || frame.Height <= 0 {
		return ""
	}

	var out []string
	out = append(out, frame.Header...)

	bodyWidth := frame.Width - frame.SidebarWidth - 1
	divider := theme.Divider.Render("│")

	rows := max(0, frame.Height-len(frame.Header)-len(frame.Composer)-len(frame.Picker)-2)
	for row := 0; row < rows; row++ {
		left := ""
		if row < len(frame.Sidebar) {
			left = frame.Sidebar[row]
		}
		right := ""
		if row < len(frame.Body) {
			right = frame.Body[row]
		}
		out = append(out, PadCells(left, frame.SidebarWidth)+divider+PadCells(right, bodyWidth))
	}

	out = append(out, frame.Picker...)
	out = append(out, frame.Typing)
	out = append(out, frame.Composer...)
	out = append(out, frame.Status)

	// Never emit more lines than the terminal has, or the screen scrolls.
	if len(out) > frame.Height {
		out = out[:frame.Height]
	}
	return strings.Join(out, "\n")
}

// PadCells pads a possibly-styled string to width printable cells. Styled text
// cannot be padded with Pad, which would truncate inside escape sequences.
func PadCells(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if gap := width - Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// Window slices lines to a visible viewport of height rows starting at offset,
// padding with blanks so the result is always exactly height long.
func Window(lines []string, offset, height int) []string {
	if height <= 0 {
		return nil
	}
	out := make([]string, 0, height)
	for row := 0; row < height; row++ {
		index := offset + row
		if index >= 0 && index < len(lines) {
			out = append(out, lines[index])
			continue
		}
		out = append(out, "")
	}
	return out
}

// Centered renders lines inside a full-screen box, vertically and horizontally
// centred. Used for the login form and the help overlay.
func Centered(lines []string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	contentWidth := 0
	for _, line := range lines {
		contentWidth = max(contentWidth, Width(line))
	}
	leftPad := max(0, (width-contentWidth)/2)
	topPad := max(0, (height-len(lines))/2)

	out := make([]string, 0, height)
	for row := 0; row < topPad; row++ {
		out = append(out, "")
	}
	indent := strings.Repeat(" ", leftPad)
	for _, line := range lines {
		out = append(out, indent+line)
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out[:height], "\n")
}
