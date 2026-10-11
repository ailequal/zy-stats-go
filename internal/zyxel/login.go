package zyxel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	pathRSAPublicKey = "/getRSAPublickKey" // sic, firmware spelling
	pathUserLogin    = "/UserLogin"
	pathUserLogout   = "/cgi-bin/UserLogout"
	rsaKeyNone       = "None"
)

// Login runs the plaintext handshake verified on the FWA505:
//
//  1. GET /getRSAPublickKey. Over http a real key means the firmware expects
//     the AES/RSA login, which is not implemented: ErrEncryptionRequired.
//     Over https the web UI itself never encrypts, so any key is ignored.
//  2. POST /UserLogin with the compact JSON body the browser sends
//     (Input_Account, base64 Input_Passwd, currLang, RememberPassword,
//     SHA512_password) and the browser's headers. The Session cookie lands in
//     the jar; the sessionkey of the body is stored for Logout.
//
// A second call replaces the session. Failures map the firmware "result" to
// ErrInvalidCredentials, ErrAccountLocked, ErrSessionLimit or ErrUnauthorized
// inside an APIError.
func (c *Client) Login(ctx context.Context, username, password string) error {
	const op = "login"
	c.setSessionKey("")

	key, err := c.rsaPublicKey(ctx)
	if err != nil {
		return err
	}
	if c.base.Scheme == "http" && key != "" && key != rsaKeyNone {
		return fmt.Errorf("zyxel: %s: %w", op, ErrEncryptionRequired)
	}

	body := loginPayload(username, password)
	resp, err := c.do(ctx, op, http.MethodPost, pathUserLogin, nil, loginContentType, body, map[string]string{
		"If-Modified-Since": headerIfModified,
	})
	if err != nil {
		return err
	}
	if IsEncryptedEnvelope(resp.body) {
		return fmt.Errorf("zyxel: %s: %w", op, ErrEncryptedEnvelope)
	}
	var out struct {
		SessionKey RawScalar `json:"sessionkey"`
		Result     string    `json:"result"`
	}
	_ = json.Unmarshal(resp.body, &out) // a non-JSON body is handled through the status below
	if resp.status != http.StatusOK || (out.Result != "" && out.Result != resultSuccess) {
		return &APIError{Op: op, Status: resp.status, Result: out.Result, Err: loginError(out.Result)}
	}
	if !out.SessionKey.IsSet() || out.SessionKey.String() == "" {
		return &APIError{Op: op, Status: resp.status, Result: out.Result, Err: fmt.Errorf("%w: no sessionkey in the login response", ErrUnauthorized)}
	}
	c.setSessionKey(out.SessionKey.String())
	return nil
}

// rsaPublicKey returns the RSAPublicKey value; "" when the call fails with a
// non-200 status or a non-JSON body (treated as "no key").
func (c *Client) rsaPublicKey(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, "login", http.MethodGet, pathRSAPublicKey, nil, "", nil, nil)
	if err != nil {
		return "", err
	}
	if resp.status != http.StatusOK {
		return "", nil
	}
	var out struct {
		RSAPublicKey string `json:"RSAPublicKey"`
	}
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return "", nil //nolint:nilerr // a non-JSON answer means no usable key
	}
	return strings.TrimSpace(out.RSAPublicKey), nil
}

// loginPayload builds the exact browser body, key order included.
func loginPayload(username, password string) []byte {
	user, _ := json.Marshal(username)
	pw, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte(password)))
	return []byte(`{"Input_Account":` + string(user) + `,"Input_Passwd":` + string(pw) +
		`,"currLang":"en","RememberPassword":0,"SHA512_password":false}`)
}

// loginError maps the firmware result strings seen in the web UI code.
func loginError(result string) error {
	switch strings.TrimSpace(result) {
	case "Invalid Username or Password":
		return ErrInvalidCredentials
	case "Locked User":
		return ErrAccountLocked
	case "Duplicated login", "Maxium number of login account has reached", "Maximum number of login account has reached":
		return ErrSessionLimit
	default:
		return ErrUnauthorized
	}
}

// Logout posts /cgi-bin/UserLogout?sessionkey=<k> with the Session cookie
// (verified on the FWA505: 200, no CSRFToken header, no GET fallback). The
// stored sessionkey is cleared whatever the outcome; the caller treats the
// error as best effort.
func (c *Client) Logout(ctx context.Context) error {
	const op = "logout"
	key := c.SessionKey()
	c.setSessionKey("")
	if key == "" {
		return fmt.Errorf("zyxel: %s: %w", op, ErrSessionExpired)
	}
	resp, err := c.do(ctx, op, http.MethodPost, pathUserLogout, url.Values{"sessionkey": {key}}, "", nil, nil)
	if err != nil {
		return err
	}
	if resp.status != http.StatusOK {
		return &APIError{Op: op, Status: resp.status, Result: resultOf(resp.body), Err: statusError(resp.status)}
	}
	return nil
}
