// Package zyxel talks to the web API of a Zyxel FWA505 router: plaintext
// login handshake over HTTPS, session cookie, UserLoginCheck, cellwan_status
// and logout.
package zyxel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Defaults of the HTTP client.
const (
	DefaultTimeout   = 15 * time.Second
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"

	headerAccept         = "application/json, text/javascript, */*; q=0.01"
	headerRequestedWith  = "XMLHttpRequest"
	headerIfModified     = "Thu, 01 Jun 1970 00:00:00 GMT"
	loginContentType     = "application/x-www-form-urlencoded; charset=UTF-8"
	pathLoginCheck       = "/cgi-bin/UserLoginCheck"
	pathDAL              = "/cgi-bin/DAL"
	oidCellwanStatus     = "cellwan_status"
	resultSuccess        = "ZCFG_SUCCESS"
	maxResponseBodyBytes = 4 << 20
)

// Client talks to one router. It is safe for use by one goroutine at a time
// plus concurrent readers of SessionKey.
type Client struct {
	base      *url.URL
	hc        *http.Client
	timeout   time.Duration
	debug     io.Writer
	userAgent string

	mu         sync.Mutex
	sessionKey string
}

// Option configures New.
type Option func(*Client)

// WithHTTPClient replaces the whole http.Client (transport, cookie jar,
// timeout, redirect policy). Meant for tests.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithTimeout sets the per-request timeout of the default http.Client.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithDebug writes redacted request and response dumps to w; nil disables.
func WithDebug(w io.Writer) Option { return func(c *Client) { c.debug = w } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// New validates and normalises baseURL (scheme http or https, host required,
// trailing slashes trimmed) and builds the default http.Client: cookie jar,
// TLS InsecureSkipVerify (self-signed router certificate), no proxy,
// redirects never followed, MaxIdleConnsPerHost 2, 15 s timeout. http is
// accepted for the fake modem; Login refuses a real RSA key over http.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("zyxel: base URL %q: %w", baseURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("zyxel: base URL %q must be http(s)://host[:port]", baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath, u.RawQuery, u.Fragment, u.User = "", "", "", nil

	c := &Client{base: u, timeout: DefaultTimeout, userAgent: DefaultUserAgent}
	for _, o := range opts {
		o(c)
	}
	if c.hc == nil {
		c.hc, err = defaultHTTPClient(c.timeout)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func defaultHTTPClient(timeout time.Duration) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("zyxel: cookie jar: %w", err)
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // LAN device with a self-signed certificate
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{
		Transport:     transport,
		Jar:           jar,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// BaseURL returns the normalised base URL.
func (c *Client) BaseURL() string { return c.base.String() }

// SessionKey returns the sessionkey of the current session, "" before login.
func (c *Client) SessionKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionKey
}

func (c *Client) setSessionKey(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionKey = k
}

// LoginCheck calls GET /cgi-bin/UserLoginCheck and returns the raw JSON body.
// Any non-200 status is ErrUnauthorized (the app prints
// "Login check failed. Check your credentials in the .env file.").
func (c *Client) LoginCheck(ctx context.Context) (json.RawMessage, error) {
	const op = "logincheck"
	resp, err := c.do(ctx, op, http.MethodGet, pathLoginCheck, nil, "", nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.status != http.StatusOK {
		return nil, &APIError{Op: op, Status: resp.status, Result: resultOf(resp.body), Err: ErrUnauthorized}
	}
	c.adoptSessionKey(resp.body)
	return json.RawMessage(resp.body), nil
}

// CellwanStatus calls GET /cgi-bin/DAL?oid=cellwan_status and returns the
// parsed response. 401, 403, 3xx and 504 map to ErrSessionExpired, other
// non-200 statuses to ErrUnexpectedStatus. An encrypted envelope is reported as
// ErrEncryptedEnvelope (it is not decrypted). "result" is not
// checked; the call fails with ErrNoData when Object is empty. The current
// sessionkey is sent as &sessionkey=: firmwares that check it on DAL reads
// need it, and the FWA505 ignores it. A sessionkey in the body replaces the
// stored one.
func (c *Client) CellwanStatus(ctx context.Context) (*CellwanStatusResponse, error) {
	const op = "cellwan_status"
	query := url.Values{"oid": {oidCellwanStatus}}
	if k := c.SessionKey(); k != "" {
		query.Set("sessionkey", k)
	}
	resp, err := c.do(ctx, op, http.MethodGet, pathDAL, query, "", nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.status != http.StatusOK {
		return nil, &APIError{Op: op, Status: resp.status, Result: resultOf(resp.body), Err: statusError(resp.status)}
	}
	if IsEncryptedEnvelope(resp.body) {
		return nil, fmt.Errorf("zyxel: %s: %w", op, ErrEncryptedEnvelope)
	}
	var out CellwanStatusResponse
	if err := json.Unmarshal(resp.body, &out); err != nil {
		return nil, fmt.Errorf("zyxel: %s: decode response: %w", op, err)
	}
	c.adoptSessionKey(resp.body)
	if len(out.Object) == 0 {
		return nil, fmt.Errorf("zyxel: %s (%s): %w", op, out.Result, ErrNoData)
	}
	return &out, nil
}

// statusError maps a non-200 API status to a sentinel. 504 is included:
// observed on the FWA505 (2026-09-17), the first call after the idle timeout
// answers 504 {"result":"Timeout"} and only later calls answer 401, so a
// 504 on a read is the router saying the session is gone.
func statusError(status int) error {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusGatewayTimeout:
		return ErrSessionExpired
	case status >= 300 && status < 400:
		return ErrSessionExpired
	}
	return ErrUnexpectedStatus
}

// resultOf extracts the firmware "result" string from a JSON body, "" when absent.
func resultOf(body []byte) string {
	var probe struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Result
}

// adoptSessionKey stores a "sessionkey" found in body (some firmwares rotate
// it). Numbers are kept as their literal text.
func (c *Client) adoptSessionKey(body []byte) {
	var probe struct {
		SessionKey RawScalar `json:"sessionkey"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || !probe.SessionKey.IsSet() {
		return
	}
	if k := probe.SessionKey.String(); k != "" {
		c.setSessionKey(k)
	}
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// do performs one request with the common headers, reads the body (capped)
// and dumps both sides in debug mode. Transport errors are wrapped with op.
func (c *Client) do(ctx context.Context, op, method, path string, query url.Values, contentType string, body []byte, extra map[string]string) (*response, error) {
	u := *c.base
	u.Path = c.base.Path + path
	u.RawQuery = query.Encode()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("zyxel: %s: build request: %w", op, err)
	}
	req.Header.Set("Accept", headerAccept)
	req.Header.Set("X-Requested-With", headerRequestedWith)
	req.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	c.dumpRequest(req)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zyxel: %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("zyxel: %s: read response: %w", op, err)
	}
	c.dumpResponse(resp, data)
	return &response{status: resp.StatusCode, header: resp.Header, body: data}, nil
}

var redactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(Cookie: Session=)[^;\r\n]*`),
	regexp.MustCompile(`(?i)(Set-Cookie: Session=)[^;\r\n]*`),
	regexp.MustCompile(`("Input_Passwd"\s*:\s*")[^"]*`),
	regexp.MustCompile(`("content"\s*:\s*")[^"]*`),
	regexp.MustCompile(`("key"\s*:\s*")[^"]*`),
	regexp.MustCompile(`("sessionkey"\s*:\s*"?)[^",}\s]*`),
	regexp.MustCompile(`([?&]sessionkey=)[^&\s]*`),
}

// Redact hides credentials, cookies, sessionkeys and encrypted payloads in a
// dump.
func Redact(s string) string {
	for _, re := range redactions {
		s = re.ReplaceAllString(s, "${1}<redacted>")
	}
	return s
}

// dumpRequest writes the outgoing request. Cookies from the jar are added by
// http.Client later, so they never appear here; explicit headers are redacted.
func (c *Client) dumpRequest(req *http.Request) {
	if c.debug == nil {
		return
	}
	dump, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		_, _ = fmt.Fprintf(c.debug, "--- request dump failed: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(c.debug, "--- request\n%s\n", Redact(string(dump)))
}

func (c *Client) dumpResponse(resp *http.Response, body []byte) {
	if c.debug == nil {
		return
	}
	saved := resp.Body
	resp.Body = io.NopCloser(bytes.NewReader(body))
	dump, err := httputil.DumpResponse(resp, true)
	resp.Body = saved
	if err != nil {
		_, _ = fmt.Fprintf(c.debug, "--- response dump failed: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(c.debug, "--- response\n%s\n", Redact(string(dump)))
}
