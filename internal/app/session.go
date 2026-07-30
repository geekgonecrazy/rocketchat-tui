package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// Session is an authenticated client plus the identity behind it.
type Session struct {
	Client      *rocket.Client
	Credentials rocket.Credentials
	Username    string
	UserID      string
}

// LoginParams describes a login attempt. Supply Token for a personal access
// token, or Username and Password (plus TOTP if the server asks) for a password
// login.
type LoginParams struct {
	ServerURL string
	Username  string
	Password  string
	TOTP      string
	Token     string
}

// Login authenticates against a server and returns a ready-to-use session.
func Login(ctx context.Context, params LoginParams) (Session, error) {
	client, err := rocket.NewClient(params.ServerURL)
	if err != nil {
		return Session{}, err
	}

	var me rocket.Me
	if params.Token != "" {
		me, err = client.LoginWithToken(ctx, params.Token)
	} else {
		if params.Username == "" || params.Password == "" {
			return Session{}, errors.New("username and password are required")
		}
		me, err = client.LoginWithPassword(ctx, params.Username, params.Password, params.TOTP)
	}
	if err != nil {
		return Session{}, err
	}

	creds := client.Credentials()
	return Session{
		Client:      client,
		Credentials: creds,
		Username:    firstNonEmpty(me.Username, creds.Username, params.Username),
		UserID:      creds.UserID,
	}, nil
}

// Resume builds a session from cached credentials without a network round trip.
// The token is validated lazily by the first API call, so a stale token surfaces
// as SessionInvalid rather than blocking startup.
func Resume(creds rocket.Credentials) (Session, error) {
	if !creds.Valid() {
		return Session{}, errors.New("no cached credentials")
	}
	client, err := rocket.NewClient(creds.ServerURL)
	if err != nil {
		return Session{}, err
	}
	client.SetCredentials(creds)
	return Session{
		Client:      client,
		Credentials: client.Credentials(),
		Username:    creds.Username,
		UserID:      creds.UserID,
	}, nil
}

// IsTOTPRequired reports whether a login failed only because a 2FA code is
// needed, so the UI can prompt for one instead of showing an error.
func IsTOTPRequired(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, rocket.ErrTOTPRequired) {
		return true
	}
	var apiErr *rocket.APIError
	return errors.As(err, &apiErr) && apiErr.TOTPRequired()
}

// LoginErrorText turns a login failure into something worth showing a user.
func LoginErrorText(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *rocket.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 401 {
			return "Incorrect username or password."
		}
		return apiErr.Message
	}
	return fmt.Sprintf("%v", err)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
