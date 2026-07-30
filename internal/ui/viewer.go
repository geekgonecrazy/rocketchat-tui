package ui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
	"github.com/geekgonecrazy/rocketchat-tui/internal/termimg"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// viewerOutcome is what the user asked for on the way out of the viewer.
type viewerOutcome int

const (
	viewerClosed viewerOutcome = iota
	viewerNext
	viewerPrev
)

// attachmentViewer draws one image full-screen, outside the Bubbletea render
// loop, and satisfies tea.ExecCommand so the program hands it the terminal for
// as long as it runs.
//
// It has to be outside the loop: image protocols write pixels the renderer
// knows nothing about, so anything it drew would be scribbled over by the next
// line-diff repaint. Bubbletea drops the alt screen when it releases the
// terminal, so the viewer pushes its own — that is what makes closing it land
// back on the unchanged chat with nothing added to the scrollback.
type attachmentViewer struct {
	attachment model.Attachment
	path       string
	protocol   termimg.Protocol

	// position is "2 of 3" when the message carries more than one attachment.
	position string
	// downloadDir is where "save" writes.
	downloadDir string

	// messageID and index say which attachment this is, so the chat can find
	// the neighbouring image when the viewer is closed with n or p.
	messageID string
	index     int

	width, height int

	stdin  io.Reader
	stdout io.Writer

	// outcome and notice are read back by the UI once Run returns.
	outcome   viewerOutcome
	notice    string
	noticeErr bool
}

func (v *attachmentViewer) SetStdin(r io.Reader)  { v.stdin = r }
func (v *attachmentViewer) SetStdout(w io.Writer) { v.stdout = w }
func (v *attachmentViewer) SetStderr(io.Writer)   {}

// Run takes over the terminal, draws, and waits for a key.
//
// It returns nil for anything the user can act on — a failed draw is reported
// through the status line, not by failing the command, because a non-nil error
// makes Bubbletea skip its own terminal restore.
func (v *attachmentViewer) Run() error {
	data, err := os.ReadFile(v.path)
	if err != nil {
		v.fail("could not read the downloaded file: %v", err)
		return nil
	}

	restore := v.takeTerminal()
	defer restore()

	for {
		v.paint(data)

		key, err := v.readKey()
		if err != nil {
			return nil
		}
		switch key {
		case keyNext:
			v.outcome = viewerNext
			return nil
		case keyPrev:
			v.outcome = viewerPrev
			return nil
		case keySave:
			v.save(data)
		case keyOpen:
			v.open()
		default:
			v.outcome = viewerClosed
			return nil
		}
	}
}

// viewerKey is the small set of actions the viewer responds to.
type viewerKey int

const (
	keyClose viewerKey = iota
	keyNext
	keyPrev
	keySave
	keyOpen
)

// takeTerminal switches to a private alt screen in raw mode and returns the
// undo. Raw mode is what lets a single keypress act without an Enter behind it.
func (v *attachmentViewer) takeTerminal() func() {
	// Alt screen, cursor hidden. The alt screen is why the chat is still intact
	// underneath and why none of this reaches the scrollback.
	io.WriteString(v.stdout, "\x1b[?1049h\x1b[?25l")

	restoreRaw := func() {}
	if file, ok := v.stdin.(interface{ Fd() uintptr }); ok {
		if state, err := term.MakeRaw(file.Fd()); err == nil {
			fd := file.Fd()
			restoreRaw = func() { term.Restore(fd, state) }
		}
	}

	return func() {
		restoreRaw()
		io.WriteString(v.stdout, "\x1b[?25h\x1b[?1049l")
	}
}

// paint clears the screen and redraws the image and its caption.
func (v *attachmentViewer) paint(data []byte) {
	var screen bytes.Buffer
	screen.WriteString("\x1b[2J\x1b[H")

	// Two lines at the bottom: a blank one and the caption.
	imageRows := max(v.height-2, 1)

	var picture bytes.Buffer
	cols, rows, err := termimg.Draw(&picture, v.protocol, data, v.width, imageRows)
	if err != nil {
		// Wrapped rather than truncated: the useful half of this message is the
		// end, where it says which key to press instead.
		picture.Reset()
		wrapped := render.Wrap(describeDrawError(err, v.protocol), v.width)
		for _, line := range wrapped {
			picture.WriteString(v.centred(line))
			picture.WriteString("\r\n")
		}
		// Each line is centred already; claiming the full width stops indent
		// shifting them a second time.
		cols, rows = v.width, len(wrapped)
	}

	for range max((imageRows-rows)/2, 0) {
		screen.WriteString("\r\n")
	}
	screen.Write(indent(picture.Bytes(), max((v.width-cols)/2, 0), v.protocol))

	screen.WriteString("\x1b[" + fmt.Sprint(v.height) + ";1H")
	screen.WriteString(v.caption(data))

	v.stdout.Write(screen.Bytes())
}

// caption is the bottom line: what this is, and what the keys do.
func (v *attachmentViewer) caption(data []byte) string {
	// Ordered by how much the user loses if it is cut: which of how many they
	// are looking at matters more than the file's exact byte count.
	facts := []string{v.attachment.Filename()}
	if v.position != "" {
		facts = append(facts, v.position)
	}
	if config, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		facts = append(facts, fmt.Sprintf("%d×%d", config.Width, config.Height))
	}
	facts = append(facts, render.HumanBytes(int64(len(data))))

	line := strings.Join(facts, "  ")
	if v.notice != "" {
		line = v.notice
	}

	keys := "q back · s save · o open"
	if v.position != "" {
		keys = "q back · n/p cycle · s save · o open"
	}

	// On a narrow terminal, squeeze the description rather than dropping it:
	// the keys are a fixed cost and what is left over is still enough to say
	// which file this is.
	if render.Width(keys)+4 > v.width {
		return render.Truncate(keys, v.width)
	}
	line = render.Truncate(line, v.width-render.Width(keys)-2)
	gap := v.width - render.Width(line) - render.Width(keys)
	return line + strings.Repeat(" ", gap) + keys
}

// indent shifts drawn output right by pad columns, to centre it.
//
// How depends on the back end: the block renderer emits one line of cells per
// row, so each needs its own left margin, whereas the graphics protocols place
// a single image wherever the cursor happens to be, so the cursor moves once.
func indent(drawn []byte, pad int, protocol termimg.Protocol) []byte {
	if pad <= 0 {
		return drawn
	}
	if protocol != termimg.Blocks {
		return append([]byte("\x1b["+strconv.Itoa(pad)+"C"), drawn...)
	}

	margin := []byte(strings.Repeat(" ", pad))
	lines := bytes.Split(drawn, []byte("\r\n"))
	for i, line := range lines {
		if len(line) > 0 {
			lines[i] = append(append([]byte{}, margin...), line...)
		}
	}
	return bytes.Join(lines, []byte("\r\n"))
}

// centred pads a single line to the middle of the screen.
func (v *attachmentViewer) centred(text string) string {
	text = render.Truncate(text, v.width)
	pad := max((v.width-render.Width(text))/2, 0)
	return strings.Repeat(" ", pad) + text
}

// readKey blocks for one keypress.
//
// It reads into a buffer rather than a single byte because arrow keys and the
// like arrive as a multi-byte escape sequence in one write; taking only the
// first byte would leave the remainder in the pipe for the restored chat screen
// to interpret as stray keystrokes.
func (v *attachmentViewer) readKey() (viewerKey, error) {
	buf := make([]byte, 16)
	n, err := v.stdin.Read(buf)
	if err != nil || n == 0 {
		return keyClose, err
	}

	switch pressed := buf[:n]; {
	case bytes.Equal(pressed, []byte("\x1b[C")), bytes.Equal(pressed, []byte("\x1b[B")):
		return keyNext, nil
	case bytes.Equal(pressed, []byte("\x1b[D")), bytes.Equal(pressed, []byte("\x1b[A")):
		return keyPrev, nil
	}

	switch buf[0] {
	case 'n', 'j', 'l', ' ':
		return keyNext, nil
	case 'p', 'k', 'h':
		return keyPrev, nil
	case 's':
		return keySave, nil
	case 'o':
		return keyOpen, nil
	default:
		// Everything else closes, so no one is ever trapped in here hunting for
		// the exit.
		return keyClose, nil
	}
}

// save copies the attachment out of the cache and into the download directory.
func (v *attachmentViewer) save(data []byte) {
	path, err := saveAttachment(v.downloadDir, v.attachment.Filename(), data)
	if err != nil {
		v.fail("%v", err)
		return
	}
	v.notice, v.noticeErr = "saved to "+shortenPath(path), false
}

// open hands the cached file to the desktop's opener.
func (v *attachmentViewer) open() {
	if err := openExternally(v.path); err != nil {
		v.fail("%v", err)
		return
	}
	v.notice, v.noticeErr = "opened "+v.attachment.Filename(), false
}

func (v *attachmentViewer) fail(format string, args ...any) {
	v.notice, v.noticeErr = fmt.Sprintf(format, args...), true
}

// describeDrawError turns a draw failure into something actionable, because
// "unsupported image format" on its own does not tell the user what to do next.
func describeDrawError(err error, protocol termimg.Protocol) string {
	if errors.Is(err, termimg.ErrUndecodable) {
		if protocol == termimg.Blocks {
			return "this terminal cannot draw images and rctui cannot decode this format — press o to open it"
		}
		return "rctui cannot decode this image format — press o to open it"
	}
	return "could not draw this image: " + err.Error()
}

// saveAttachment writes data into dir under name, without ever replacing a file
// that is already there — a download is not worth losing someone's work over,
// and two uploads sharing a name is ordinary.
func saveAttachment(dir, name string, data []byte) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no download directory configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", shortenPath(dir), err)
	}

	path := filepath.Join(dir, name)
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for attempt := 1; ; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			if attempt > 99 {
				return "", fmt.Errorf("too many files named like %s already", name)
			}
			path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, attempt, extension))
			continue
		}
		if err != nil {
			return "", fmt.Errorf("could not write to %s: %w", shortenPath(dir), err)
		}
		defer file.Close()
		if _, err := file.Write(data); err != nil {
			return "", fmt.Errorf("could not write %s: %w", shortenPath(path), err)
		}
		return path, nil
	}
}

// openExternally launches the platform's file opener, detached from our
// terminal so a chatty helper cannot scribble over the screen we are drawing.
func openExternally(path string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux", "freebsd", "openbsd", "netbsd":
		command = "xdg-open"
	case "windows":
		command = "explorer"
	default:
		return fmt.Errorf("no way to open files on %s", runtime.GOOS)
	}

	binary, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("%s is not installed, so there is nothing to open files with", command)
	}
	cmd := exec.Command(binary, path)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not launch %s: %w", command, err)
	}
	// Reap the child rather than leaving a zombie; the opener returns as soon
	// as it has handed off, so this does not outlive the keystroke.
	go cmd.Wait()
	return nil
}

// shortenPath abbreviates the home directory, so status lines stay readable.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, found := strings.CutPrefix(path, home+string(os.PathSeparator)); found {
		return "~" + string(os.PathSeparator) + rest
	}
	return path
}
