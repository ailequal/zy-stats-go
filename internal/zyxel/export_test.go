package zyxel

import "net/http"

// HTTPClient exposes the underlying http.Client to the package tests, so they
// can check that WithHTTPClient is honoured.
func (c *Client) HTTPClient() *http.Client { return c.hc }
