// Package solver talks to a FlareSolverr instance to obtain a fresh
// cf_clearance cookie.
//
// ext.to sits behind Cloudflare. A clearance cookie is bound to the client's
// IP address and User-Agent, so it cannot simply be copied from an arbitrary
// browser: it has to be issued to the same egress IP that will replay it, with
// the User-Agent that produced it. Running the solver as a sidecar container
// alongside the forwarder satisfies both, because both share one NAT address.
//
// The cookie is still harvested by a real browser (inside FlareSolverr) rather
// than computed here, which is the only approach that works for a managed
// challenge.
package solver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured reports that no solver endpoint is set, which disables
// automatic cookie refresh rather than failing a scan.
var ErrNotConfigured = errors.New("未配置 FlareSolverr 地址")

// Client calls a FlareSolverr endpoint.
type Client struct {
	// Endpoint is the full /v1 URL, for example http://flaresolverr:8191/v1.
	Endpoint string
	HTTP     *http.Client
}

// New builds a client. An empty endpoint yields ErrNotConfigured from Solve.
func New(endpoint string) *Client {
	return &Client{
		Endpoint: strings.TrimSpace(endpoint),
		// Solving a managed challenge can take a while; the deadline is the
		// solver's own maxTimeout, so the transport timeout only has to be
		// larger than the longest allowed solve.
		HTTP: &http.Client{Timeout: 150 * time.Second},
	}
}

// Configured reports whether an endpoint is set.
func (c *Client) Configured() bool {
	return c != nil && c.Endpoint != ""
}

// Cookie is one cookie from the solver's browser context.
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

// Solution is the replay bundle for a passed challenge.
type Solution struct {
	// Clearance is the cf_clearance value.
	Clearance string
	// Session is the PHPSESSID value, empty when the site did not set one.
	Session string
	// UserAgent is the browser identity the clearance is bound to. It must be
	// sent with every subsequent request.
	UserAgent string
	// Cookies is the full cookie list, kept for logging and diagnostics.
	Cookies []Cookie
	// Elapsed is how long the solve took, when the solver reports it.
	Elapsed float64
	// Message is the solver's own status line, for the operator.
	Message string
}

// request mirrors the FlareSolverr v1 request body.
type request struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type response struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		Response  string   `json:"response"`
		UserAgent string   `json:"userAgent"`
		Cookies   []Cookie `json:"cookies"`
	} `json:"solution"`
}

// Solve asks the solver to load targetURL and return the cookie bundle it
// received after passing the challenge.
//
// The request goes to the very page the caller intends to scan, because
// Cloudflare issues a clearance for the challenge it actually served; asking
// for the site root would leave the listing path to be challenged again.
func (c *Client) Solve(ctx context.Context, targetURL string) (Solution, error) {
	if !c.Configured() {
		return Solution{}, ErrNotConfigured
	}
	if _, err := url.Parse(targetURL); err != nil {
		return Solution{}, fmt.Errorf("目标地址无效：%w", err)
	}

	body, err := json.Marshal(request{
		Cmd:        "request.get",
		URL:        targetURL,
		MaxTimeout: 90_000,
	})
	if err != nil {
		return Solution{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Solution{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Solution{}, fmt.Errorf("无法连接 FlareSolverr（%s）：%w", c.Endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Solution{}, err
	}

	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Solution{}, fmt.Errorf("FlareSolverr 返回了无法解析的内容（HTTP %d）：%s",
			resp.StatusCode, truncate(string(raw), 160))
	}
	if out.Status != "ok" {
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return Solution{}, fmt.Errorf("FlareSolverr 未能通过验证：%s", msg)
	}

	sol := Solution{UserAgent: out.Solution.UserAgent, Cookies: out.Solution.Cookies, Message: out.Message}
	for _, ck := range out.Solution.Cookies {
		switch ck.Name {
		case "cf_clearance":
			sol.Clearance = ck.Value
		case "PHPSESSID":
			sol.Session = ck.Value
		}
	}
	if sol.Clearance == "" {
		// status ok without a clearance means the page loaded but no challenge
		// was solved, which happens when an existing cookie in the solver's
		// browser was still valid. The caller has nothing to store, so this is
		// reported instead of silently writing an empty cookie.
		return sol, errors.New("FlareSolverr 已通过验证，但没有返回 cf_clearance")
	}
	return sol, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
