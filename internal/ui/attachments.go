package ui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/model"
)

// attachmentAction is what to do with an attachment once it is on disk.
type attachmentAction int

const (
	actionView attachmentAction = iota
	actionSave
	actionOpen
)

// pendingAttachment is a fetch in flight. Downloads are asynchronous, so the
// request has to remember what it was for and which attachment it was: by the
// time bytes arrive the user may have moved the cursor, changed room, or asked
// for something else entirely, and a stale result must not open a viewer.
type pendingAttachment struct {
	messageID string
	index     int
	action    attachmentAction
	active    bool
}

// viewerFinishedMsg reports that the full-screen viewer has given the terminal
// back, and what the user did on the way out.
type viewerFinishedMsg struct {
	viewer *attachmentViewer
	err    error
}

// requestAttachment is the entry point for the v/s/o keys: it works out which
// attachment the cursor is on and asks the core to fetch it.
func (m chatModel) requestAttachment(action attachmentAction) (chatModel, tea.Cmd) {
	message, ok := m.selectedMessageValue()
	if !ok {
		return m.notify("select a message first", false)
	}

	candidates := attachmentsFor(message, action)
	if len(candidates) == 0 {
		return m.notify(noAttachmentReason(message, action), false)
	}
	return m.fetchAttachment(message.ID, candidates[0], action)
}

// fetchAttachment asks the core for one attachment by its index within the
// message's list, and remembers what to do when it lands.
func (m chatModel) fetchAttachment(messageID string, target indexedAttachment, action attachmentAction) (chatModel, tea.Cmd) {
	m.pending = pendingAttachment{
		messageID: messageID,
		index:     target.index,
		action:    action,
		active:    true,
	}
	m.core.FetchAttachment(messageID, target.index, target.attachment)

	// Only announce a fetch that will actually take a moment. A cached file
	// comes back within the same frame, and a "fetching…" that is replaced
	// before it is ever drawn just makes the status bar flicker.
	return m.notify("fetching "+target.attachment.Filename()+"…", false)
}

// attachmentFetched handles the download landing.
func (m chatModel) attachmentFetched(e app.AttachmentFetched) (chatModel, tea.Cmd) {
	if !m.pending.active || m.pending.messageID != e.MessageID || m.pending.index != e.Index {
		// Something else was asked for since; this result is no longer wanted.
		return m, nil
	}
	m.pending.active = false

	if e.Err != nil {
		return m.notify(e.Err.Error(), true)
	}

	switch m.pending.action {
	case actionSave:
		data, err := os.ReadFile(e.Path)
		if err != nil {
			return m.notify("could not read the downloaded file: "+err.Error(), true)
		}
		path, err := saveAttachment(m.downloadDir, e.Attachment.Filename(), data)
		if err != nil {
			return m.notify(err.Error(), true)
		}
		return m.notifyLong("saved to "+shortenPath(path), false)

	case actionOpen:
		if err := openExternally(e.Path); err != nil {
			return m.notify(err.Error(), true)
		}
		return m.notify("opened "+e.Attachment.Filename(), false)

	default:
		return m.openViewer(e)
	}
}

// openViewer hands the terminal to the full-screen viewer.
func (m chatModel) openViewer(e app.AttachmentFetched) (chatModel, tea.Cmd) {
	viewer := &attachmentViewer{
		attachment:  e.Attachment,
		path:        e.Path,
		protocol:    m.imageProtocol,
		position:    m.viewerPosition(e.MessageID, e.Index),
		downloadDir: m.downloadDir,
		width:       m.width,
		height:      m.height,
		messageID:   e.MessageID,
		index:       e.Index,
	}
	m.notice, m.noticeErr = "", false
	return m, tea.Exec(viewer, func(err error) tea.Msg {
		return viewerFinishedMsg{viewer: viewer, err: err}
	})
}

// viewerClosed reacts to the viewer handing the terminal back: either report
// what it did, or fetch the neighbour it was asked to move to.
func (m chatModel) viewerClosed(msg viewerFinishedMsg) (chatModel, tea.Cmd) {
	if msg.err != nil {
		return m.notify("image viewer: "+msg.err.Error(), true)
	}

	if step := viewerStep(msg.viewer.outcome); step != 0 {
		if next, ok := m.neighbourImage(msg.viewer.messageID, msg.viewer.index, step); ok {
			return m.fetchAttachment(msg.viewer.messageID, next, actionView)
		}
	}
	if msg.viewer.notice != "" {
		return m.notify(msg.viewer.notice, msg.viewer.noticeErr)
	}
	return m, nil
}

func viewerStep(outcome viewerOutcome) int {
	switch outcome {
	case viewerNext:
		return 1
	case viewerPrev:
		return -1
	default:
		return 0
	}
}

// viewerPosition labels which image of how many is showing, and is empty when
// there is only one — there is nothing to cycle through, so offering n/p in the
// caption would just be noise.
func (m chatModel) viewerPosition(messageID string, index int) string {
	images := m.imagesOf(messageID)
	if len(images) < 2 {
		return ""
	}
	for position, image := range images {
		if image.index == index {
			return fmt.Sprintf("%d of %d", position+1, len(images))
		}
	}
	return ""
}

// neighbourImage is the image step places away from index, wrapping around.
func (m chatModel) neighbourImage(messageID string, index, step int) (indexedAttachment, bool) {
	images := m.imagesOf(messageID)
	if len(images) < 2 {
		return indexedAttachment{}, false
	}
	for position, image := range images {
		if image.index == index {
			// Wrapping keeps a long strip of screenshots navigable in one
			// direction rather than dead-ending at either edge.
			next := ((position+step)%len(images) + len(images)) % len(images)
			return images[next], true
		}
	}
	return indexedAttachment{}, false
}

// imagesOf is the viewable images on a message, re-read from current state
// rather than captured when the viewer opened: an edit or a resync may have
// replaced the message while the viewer had the terminal.
func (m chatModel) imagesOf(messageID string) []indexedAttachment {
	message, ok := m.messageByID(messageID)
	if !ok {
		return nil
	}
	return attachmentsFor(message, actionView)
}

// indexedAttachment keeps an attachment's position in its message, which is
// what identifies it across an asynchronous fetch.
type indexedAttachment struct {
	attachment model.Attachment
	index      int
}

// attachmentsFor is the attachments on a message that the given action can act
// on: viewing needs an image, saving and opening need only something to fetch.
func attachmentsFor(message model.Message, action attachmentAction) []indexedAttachment {
	var out []indexedAttachment
	for index, attachment := range message.Attachments {
		if attachment.Source == "" {
			continue
		}
		if action == actionView && !attachment.IsImage() {
			continue
		}
		out = append(out, indexedAttachment{attachment: attachment, index: index})
	}
	return out
}

// noAttachmentReason explains why nothing happened, specifically enough that
// the user knows whether to try a different key or a different message.
func noAttachmentReason(message model.Message, action attachmentAction) string {
	switch {
	case len(message.Attachments) == 0:
		return "no attachment on this message"
	case action == actionView:
		return "that attachment is not an image — press s to save it or o to open it"
	default:
		return "that attachment has nothing to download"
	}
}

// messageByID finds a message in whichever pane is showing it.
func (m chatModel) messageByID(id string) (model.Message, bool) {
	for _, list := range [][]model.Message{m.messages, m.threadReplies, m.threads} {
		for _, message := range list {
			if message.ID == id {
				return message, true
			}
		}
	}
	if m.threadParent.ID == id {
		return m.threadParent, true
	}
	return model.Message{}, false
}

// selectedMessageValue is the message under the cursor in whichever pane has
// focus. Unlike selectedMessage it returns the message itself, because acting
// on an attachment needs the attachment list, not just an id.
func (m chatModel) selectedMessageValue() (model.Message, bool) {
	return m.messageByID(m.selectedMessage())
}

// notifyLong is notify with a longer window, for messages carrying a path the
// user may want to read down and find.
func (m chatModel) notifyLong(text string, isErr bool) (chatModel, tea.Cmd) {
	m.notice, m.noticeErr = text, isErr
	return m, tea.Tick(8*time.Second, func(time.Time) tea.Msg { return clearNoticeMsg{} })
}
