package rocket

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrTOTPRequired is returned when the server wants a 2FA code to finish login.
var ErrTOTPRequired = errors.New("rocket: two-factor code required")

type loginResponse struct {
	Status string `json:"status"`
	Data   struct {
		AuthToken string `json:"authToken"`
		UserID    string `json:"userId"`
		Me        Me     `json:"me"`
	} `json:"data"`
}

// LoginWithPassword authenticates with username (or email) and password.
// If the server requires 2FA, it returns ErrTOTPRequired; call again with a
// non-empty totp.
func (c *Client) LoginWithPassword(ctx context.Context, user, password, totp string) (Me, error) {
	body := map[string]any{"user": user, "password": password}
	headers := map[string]string{}
	if totp != "" {
		// Rocket.Chat has moved this around between versions; sending it in the
		// body and both header spellings covers 3.x through 7.x.
		body["code"] = totp
		headers["X-2fa-Code"] = totp
		headers["X-2fa-Method"] = "totp"
		headers["x-auth-method"] = "password"
	}

	var resp loginResponse
	err := c.do(ctx, request{
		method:   "POST",
		endpoint: "login",
		body:     body,
		headers:  headers,
		noAuth:   true,
	}, &resp)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.TOTPRequired() {
			return Me{}, fmt.Errorf("%w: %s", ErrTOTPRequired, apiErr.Message)
		}
		return Me{}, err
	}
	return c.adoptLogin(resp)
}

// LoginWithToken authenticates with a Personal Access Token. Rocket.Chat
// accepts a PAT as a Meteor resume token, which also tells us the user id so
// the user only has to paste one value.
func (c *Client) LoginWithToken(ctx context.Context, token string) (Me, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Me{}, fmt.Errorf("rocket: empty token")
	}
	var resp loginResponse
	err := c.do(ctx, request{
		method:   "POST",
		endpoint: "login",
		body:     map[string]any{"resume": token},
		noAuth:   true,
	}, &resp)
	if err != nil {
		return Me{}, err
	}
	return c.adoptLogin(resp)
}

func (c *Client) adoptLogin(resp loginResponse) (Me, error) {
	if resp.Data.AuthToken == "" || resp.Data.UserID == "" {
		return Me{}, fmt.Errorf("rocket: login succeeded but returned no token")
	}
	me := resp.Data.Me
	if len(me.Emails) > 0 {
		me.Email = me.Emails[0].Address
	}
	c.SetCredentials(Credentials{
		UserID:    resp.Data.UserID,
		AuthToken: resp.Data.AuthToken,
		Username:  me.Username,
	})
	return me, nil
}

// Me fetches the authenticated account, and doubles as a token validity check
// on startup when resuming from cached credentials.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	if err := c.do(ctx, request{method: "GET", endpoint: "me"}, &me); err != nil {
		return Me{}, err
	}
	if len(me.Emails) > 0 {
		me.Email = me.Emails[0].Address
	}
	// Keep the cached username in sync with the server.
	creds := c.Credentials()
	if me.Username != "" && creds.Username != me.Username {
		creds.Username = me.Username
		c.SetCredentials(creds)
	}
	return me, nil
}

// Logout invalidates the current token server-side.
func (c *Client) Logout(ctx context.Context) error {
	if !c.Authenticated() {
		return nil
	}
	err := c.do(ctx, request{method: "POST", endpoint: "logout"}, nil)
	c.SetCredentials(Credentials{})
	return err
}
