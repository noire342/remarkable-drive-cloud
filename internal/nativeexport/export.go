// SPDX-License-Identifier: AGPL-3.0-only
// Native rendering belongs to Xochitl. This package never parses ink or uses UniPDF.
package nativeexport

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var UUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c Client) Export(id, format string) (io.ReadCloser, error) {
	if !UUID.MatchString(id) || (format != "pdf" && format != "rmdoc") {
		return nil, fmt.Errorf("invalid native export")
	}
	base := c.BaseURL
	if base == "" {
		base = "http://10.11.99.1"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "10.11.99.1" && u.Hostname() != "127.0.0.1") {
		return nil, fmt.Errorf("native export requires local tablet address")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Get(base + "/download/" + id + "/" + format)
	if err != nil {
		return nil, fmt.Errorf("native export unavailable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("native export HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}
