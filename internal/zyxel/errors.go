package zyxel

import (
	"errors"
	"fmt"
)

// Sentinel errors the application reacts to.
var (
	ErrUnauthorized       = errors.New("zyxel: unauthorized")                 // generic non-200 on login/check
	ErrInvalidCredentials = errors.New("zyxel: invalid username or password") // "Invalid Username or Password"
	ErrAccountLocked      = errors.New("zyxel: account locked")               // "Locked User"
	ErrSessionLimit       = errors.New("zyxel: session limit reached")        // "Duplicated login", "Maxium number of login account has reached"
	ErrSessionExpired     = errors.New("zyxel: session expired")              // 401, 403, 3xx on API calls
	ErrEncryptedEnvelope  = errors.New("zyxel: encrypted response not supported in this version, reach the router over https")
	ErrEncryptionRequired = errors.New("zyxel: router requires the encrypted login over http; use an https:// server URL")
	ErrNoData             = errors.New("zyxel: response has no Object")
	ErrUnexpectedStatus   = errors.New("zyxel: unexpected HTTP status")
)

// APIError carries the HTTP status and the firmware "result" string of a
// failed call. It wraps one of the sentinels so errors.Is works.
type APIError struct {
	Op     string // "login", "logincheck", "cellwan_status", "logout"
	Status int
	Result string
	Err    error
}

// Error returns the operation, the HTTP status, the firmware result if any
// and the wrapped sentinel.
func (e *APIError) Error() string {
	if e.Result != "" {
		return fmt.Sprintf("zyxel: %s: HTTP %d (%s): %v", e.Op, e.Status, e.Result, e.Err)
	}
	return fmt.Sprintf("zyxel: %s: HTTP %d: %v", e.Op, e.Status, e.Err)
}

// Unwrap returns the wrapped sentinel.
func (e *APIError) Unwrap() error { return e.Err }
