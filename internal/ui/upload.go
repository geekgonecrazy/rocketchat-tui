package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// pathRows bounds how many rows of completion candidates the attach prompt
// shows, matching the other completers' habit of hinting rather than listing.
const pathRows = 3

// attachPrompt is the composer borrowed to type a file path into.
//
// It reuses the message box rather than adding a second input widget: the
// prompt and the composer are never both in use, and edit mode already
// establishes that the box can hold something other than a new message so long
// as the prompt says so and the draft comes back afterwards.
type attachPrompt struct {
	open bool
	// draft is the message the composer held when the prompt borrowed it.
	draft string
	// matches are the completion candidates for what has been typed so far.
	matches []string
}

// ---- opening and closing ------------------------------------------------------

// beginAttach opens the path prompt, seeded with initial.
func (m chatModel) beginAttach(initial string) (chatModel, tea.Cmd) {
	if m.activeRoom == "" {
		return m, nil
	}
	if m.room.ReadOnly {
		return m.notify("this room is read-only", false)
	}
	if m.editing() {
		// Rocket.Chat has no way to add a file to a message that already exists,
		// so the only honest answer is to say which of the two the user is doing.
		return m.notify("finish or cancel the edit first — a sent message can't gain a file", false)
	}

	m.attach = attachPrompt{open: true, draft: m.composer.Value()}
	m.attach.matches = pathMatches(initial)

	m.focus = focusComposer
	m.composer.Focus()
	m.composer.SetValue(initial)
	m.composer.SetHeight(1)
	m.composer.CursorEnd()
	// A path is not a message, so none of the completers apply to what is being
	// typed now; leaving one open would have it match against the path.
	m.picker.close()
	m.mentions.close()
	m.cmdPicker.close()
	m.rebuildBody()
	return m, textarea.Blink
}

// closeAttach hands the composer back with the draft it was holding.
func (m chatModel) closeAttach() chatModel {
	draft := m.attach.draft
	m.attach = attachPrompt{}
	m.composer.SetValue(draft)
	m.composer.SetHeight(clamp(strings.Count(draft, "\n")+1, 1, 4))
	m.composer.CursorEnd()
	m.rebuildBody()
	return m
}

// ---- keys ----------------------------------------------------------------------

// handleAttachKey drives the prompt. It runs ahead of the global bindings, so
// tab completes a path here instead of cycling focus.
func (m chatModel) handleAttachKey(msg tea.KeyMsg) (chatModel, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.closeAttach(), nil
	case "enter":
		return m.commitAttach()
	case "tab":
		return m.completeAttach()
	}

	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	// Candidates track the text rather than waiting for tab, so the user can see
	// whether a path exists before committing to it.
	m.attach.matches = pathMatches(m.composer.Value())
	m.rebuildBody()
	return m, cmd
}

// commitAttach queues whatever the prompt is holding.
func (m chatModel) commitAttach() (chatModel, tea.Cmd) {
	path := strings.TrimSpace(m.composer.Value())
	if path == "" {
		return m.closeAttach(), nil
	}

	upload, err := app.NewUpload(path)
	if err != nil {
		// Stay open. A rejected path is usually a nearly-right path, and closing
		// would make the user type the whole thing again to fix one character.
		return m.notify(err.Error(), true)
	}

	m.uploads = append(m.uploads, upload)
	m = m.closeAttach()
	return m.notify("attached "+upload.Name, false)
}

// completeAttach extends the path as far as the candidates agree.
func (m chatModel) completeAttach() (chatModel, tea.Cmd) {
	completed, matches := completePath(m.composer.Value())
	m.attach.matches = matches
	m.composer.SetValue(completed)
	m.composer.CursorEnd()
	m.rebuildBody()
	return m, nil
}

// dropLastUpload removes the most recently attached file.
//
// Last rather than a selected one: the queue is a short list built moments ago,
// and giving it a cursor would mean another focusable thing to tab through for
// an action that is almost always "undo that".
func (m chatModel) dropLastUpload() (chatModel, tea.Cmd) {
	if len(m.uploads) == 0 {
		return m, nil
	}
	dropped := m.uploads[len(m.uploads)-1]
	m.uploads = m.uploads[:len(m.uploads)-1]
	m.rebuildBody()
	return m.notify("removed "+dropped.Name, false)
}

// attachFromCommand handles "/upload <path>" without opening the prompt.
//
// The path is whatever followed the command, taken verbatim rather than split on
// spaces, so a name with spaces in it needs no quoting.
func (m chatModel) attachFromCommand(path string) (chatModel, tea.Cmd) {
	if path == "" {
		// Bare "/upload" means the user wants the picker, not an error.
		m.composer.Reset()
		m.composer.SetHeight(1)
		return m.beginAttach("")
	}

	upload, err := app.NewUpload(path)
	if err != nil {
		// The composer keeps the command so the path can be corrected in place.
		return m.notify(err.Error(), true)
	}
	m.uploads = append(m.uploads, upload)
	m.composer.Reset()
	m.composer.SetHeight(1)
	m.rebuildBody()
	return m.notify("attached "+upload.Name, false)
}

// ---- labels ---------------------------------------------------------------------

// attachSuggestions is the completion strip shown while the prompt is open. It
// is rendered rather than counted so that layout and drawing cannot disagree
// about how many rows it takes.
func (m chatModel) attachSuggestions() []string {
	return render.PathMatches(m.theme, render.PathMatchesState{
		Matches: m.attach.matches,
		Width:   m.width,
		MaxRows: pathRows,
	})
}

// uploadLabels renders the queue for the composer's chip line.
func (m chatModel) uploadLabels() []string {
	if len(m.uploads) == 0 {
		return nil
	}
	labels := make([]string, 0, len(m.uploads))
	for _, upload := range m.uploads {
		labels = append(labels, upload.Name+" ("+render.HumanBytes(upload.Size)+")")
	}
	return labels
}

// ---- path completion --------------------------------------------------------------

// completePath extends input as far as its candidates agree, and returns them
// so the prompt can show what it is choosing between.
//
// Shell semantics: a unique match completes fully, several complete to their
// common prefix, and none leaves the text alone. Directories gain a trailing
// slash so the next tab descends rather than stalling on a name that is already
// complete.
func completePath(input string) (string, []string) {
	// "~" on its own has no directory to list yet; taking it to the home
	// directory is what the user meant and is what the next tab needs.
	if strings.TrimSpace(input) == "~" {
		return "~/", pathMatches("~/")
	}

	head, prefix := splitPathPrefix(input)
	matches := pathMatches(input)
	if len(matches) == 0 {
		return input, nil
	}

	shared := matches[0]
	for _, match := range matches[1:] {
		shared = commonPrefix(shared, match)
	}
	if len(shared) <= len(prefix) {
		// Nothing further to add — the candidates diverge right where the cursor
		// is. Show them and leave the text as typed.
		return input, matches
	}
	return head + shared, matches
}

// pathMatches lists the directory entries input could still become.
func pathMatches(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	head, prefix := splitPathPrefix(input)

	dir, err := app.ExpandPath(head)
	if err != nil {
		return nil
	}
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		// Dotfiles stay out of the way until they are asked for by name, the way
		// a shell hides them: a home directory is otherwise mostly configuration.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if isDir(entry, filepath.Join(dir, name)) {
			name += "/"
		}
		matches = append(matches, name)
	}
	sort.Strings(matches)
	return matches
}

// isDir reports whether an entry leads somewhere, following symlinks: a link to
// a directory should complete with a slash like the directory it points at.
func isDir(entry os.DirEntry, path string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// splitPathPrefix cuts input at the last separator into the part that names a
// directory and the part still being typed.
//
// It splits the raw input rather than an expanded copy so that a leading "~"
// survives completion instead of being rewritten into the user's home path
// under them.
func splitPathPrefix(input string) (head, prefix string) {
	if slash := strings.LastIndexByte(input, '/'); slash >= 0 {
		return input[:slash+1], input[slash+1:]
	}
	return "", input
}

func commonPrefix(a, b string) string {
	limit := min(len(a), len(b))
	for i := range limit {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:limit]
}
