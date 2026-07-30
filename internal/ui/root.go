package ui

import (
	"context"
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/app"
	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui/render"
)

// coreEventMsg carries one app event into the Bubbletea loop.
//
// The core publishes on a channel and the UI pulls one event per Update via a
// re-arming command. That is what keeps the two loops independent: a burst of
// server activity can never block a redraw, and a slow redraw can never block
// the network.
type coreEventMsg struct{ event app.Event }

// coreClosedMsg means the core has shut down.
type coreClosedMsg struct{}

type screen int

const (
	screenLogin screen = iota
	screenChat
)

// Root switches between the login form and the chat screen and owns the session
// lifecycle.
type Root struct {
	cfg    *config.Config
	cache  *store.Store
	theme  render.Theme
	logger *slog.Logger
	ctx    context.Context

	screen screen
	login  loginModel
	chat   chatModel

	core       *app.Core
	coreCancel context.CancelFunc

	width  int
	height int
}

// NewRoot builds the top-level model. serverOverride, when set, prefills (and
// takes precedence over) the remembered server address.
func NewRoot(ctx context.Context, cfg *config.Config, cache *store.Store, logger *slog.Logger, serverOverride string) *Root {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	theme := render.DefaultTheme()

	serverURL := cfg.ServerURL
	if serverOverride != "" {
		serverURL = serverOverride
	}

	return &Root{
		cfg:    cfg,
		cache:  cache,
		theme:  theme,
		logger: logger,
		ctx:    ctx,
		login:  newLoginModel(theme, serverURL, cfg.Username),
	}
}

func (r *Root) Init() tea.Cmd {
	// A cached token means we can go straight to the chat screen; validation
	// happens in the background and only interrupts if the token is dead.
	if r.cfg.HasSession() && r.serverMatchesConfig() {
		session, err := app.Resume(rocket.Credentials{
			ServerURL: r.cfg.ServerURL,
			UserID:    r.cfg.UserID,
			AuthToken: r.cfg.AuthToken,
			Username:  r.cfg.Username,
		})
		if err == nil {
			return r.startSession(session)
		}
		r.logger.Warn("could not resume session", "err", err)
	}
	return r.login.Init()
}

// serverMatchesConfig guards against resuming a token against a server the user
// has since pointed elsewhere with --server.
func (r *Root) serverMatchesConfig() bool {
	return r.login.inputs[fieldServer].Value() == "" ||
		shortServer(r.login.inputs[fieldServer].Value()) == shortServer(r.cfg.ServerURL)
}

func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height

	case coreClosedMsg:
		// The core closes its stream when its session context is cancelled. If we
		// have already returned to the login form, this is the tail end of the old
		// session, not a reason to quit the app.
		if r.core == nil || r.screen == screenLogin {
			return r, nil
		}
		return r, tea.Quit

	case coreEventMsg:
		// A dead session sends us back to the login form. Later failures from the
		// same session must not reset a form the user is already typing into.
		if invalid, ok := msg.event.(app.SessionInvalid); ok {
			if r.screen == screenLogin {
				return r, nil
			}
			return r, r.endSession(invalid.Reason)
		}
		var cmd tea.Cmd
		r.chat, cmd = r.chat.Update(msg)
		return r, tea.Batch(cmd, r.waitForCoreEvent())

	case loginResultMsg:
		if msg.err == nil {
			return r, r.startSession(msg.session)
		}
	}

	var cmd tea.Cmd
	switch r.screen {
	case screenLogin:
		r.login, cmd = r.login.Update(msg)
	default:
		r.chat, cmd = r.chat.Update(msg)
	}
	return r, cmd
}

func (r *Root) View() string {
	if r.screen == screenLogin {
		return r.login.View()
	}
	return r.chat.View()
}

// startSession persists credentials, boots the core, and switches to chat.
func (r *Root) startSession(session app.Session) tea.Cmd {
	r.cfg.ServerURL = session.Client.ServerURL()
	r.cfg.UserID = session.UserID
	r.cfg.AuthToken = session.Credentials.AuthToken
	r.cfg.Username = session.Username
	if err := r.cfg.Save(); err != nil {
		r.logger.Warn("could not save config", "err", err)
	}

	// A different account must never inherit the previous one's cached messages.
	accountKey := session.Client.ServerURL() + "#" + session.UserID
	if previous, err := r.cache.Meta(metaAccount); err == nil && previous != accountKey {
		if previous != "" {
			if err := r.cache.Reset(); err != nil {
				r.logger.Warn("could not reset cache", "err", err)
			}
		}
		if err := r.cache.SetMeta(metaAccount, accountKey); err != nil {
			r.logger.Warn("could not record account", "err", err)
		}
	}

	sessionCtx, cancel := context.WithCancel(r.ctx)
	r.coreCancel = cancel
	r.core = app.New(session.Client, r.cache, r.logger)
	go r.core.Run(sessionCtx)
	r.core.Start(session.UserID, session.Username)

	r.chat = newChatModel(r.core, r.theme, session.Username,
		shortServer(session.Client.ServerURL()), r.cfg.Downloads())
	r.screen = screenChat

	// Hand the new model the size it missed while the login form was up.
	var sizeCmd tea.Cmd
	if r.width > 0 && r.height > 0 {
		r.chat, sizeCmd = r.chat.Update(tea.WindowSizeMsg{Width: r.width, Height: r.height})
	}
	return tea.Batch(r.chat.Init(), sizeCmd, r.waitForCoreEvent())
}

// endSession tears the session down and returns to the login form.
func (r *Root) endSession(reason string) tea.Cmd {
	if r.coreCancel != nil {
		r.coreCancel()
		r.coreCancel = nil
	}
	r.core = nil
	r.cfg.ClearSession()
	if err := r.cfg.Save(); err != nil {
		r.logger.Warn("could not clear saved session", "err", err)
	}

	r.login = newLoginModel(r.theme, r.cfg.ServerURL, r.cfg.Username)
	r.login.message = reason
	r.screen = screenLogin

	var sizeCmd tea.Cmd
	if r.width > 0 && r.height > 0 {
		r.login, sizeCmd = r.login.Update(tea.WindowSizeMsg{Width: r.width, Height: r.height})
	}
	return tea.Batch(r.login.Init(), sizeCmd)
}

// waitForCoreEvent pulls the next core event, re-arming itself each time.
func (r *Root) waitForCoreEvent() tea.Cmd {
	if r.core == nil {
		return nil
	}
	events := r.core.Events()
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return coreClosedMsg{}
		}
		return coreEventMsg{event: event}
	}
}

const metaAccount = "account"
