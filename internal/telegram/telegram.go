// Package telegram implements the small Bot API subset needed to publish
// torrent posts to a channel or chat.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIBase is the Bot API origin, overridable for tests or a local Bot API
// server.
var APIBase = "https://api.telegram.org"

// Client talks to the Bot API with a fixed token.
type Client struct {
	Token string
	HTTP  *http.Client
}

// New returns a client with a sane timeout.
func New(token string) *Client {
	return &Client{Token: token, HTTP: &http.Client{Timeout: 120 * time.Second}}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Result      json.RawMessage `json:"result"`
}

// Message is the subset of a Telegram message we care about.
type Message struct {
	MessageID int `json:"message_id"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

// Me is the bot identity returned by getMe.
type Me struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

// GetMe validates the token and returns the bot identity.
func (c *Client) GetMe(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.call(ctx, "getMe", url.Values{}, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// Post describes one outgoing message.
type Post struct {
	ChatID string
	// ThreadID targets a specific forum topic. Zero means the general topic.
	ThreadID int
	Caption  string
	// PhotoKey is the client-side id of the photo being uploaded, if any.
	PhotoKey string
	Silent   bool
	// DisableWebPreview suppresses link previews.
	DisableWebPreview bool
}

// Send delivers a post, uploading a photo when one is attached.
//
// Captions are always sent with parse_mode=HTML. If Telegram rejects the
// entities (for example because the torrent title contains stray markup), the
// message is retried as plain text so a single bad title cannot stall the
// pipeline.
func (c *Client) Send(ctx context.Context, p Post, photo []byte, photoName string) (*Message, error) {
	if len(photo) > 0 {
		return c.sendPhoto(ctx, p, photo, photoName)
	}
	return c.sendMessage(ctx, p)
}

func (c *Client) sendMessage(ctx context.Context, p Post) (*Message, error) {
	form := url.Values{
		"chat_id":                  {p.ChatID},
		"text":                     {p.Caption},
		"parse_mode":               {"HTML"},
		"disable_notification":     {boolStr(p.Silent)},
		"disable_web_page_preview": {boolStr(p.DisableWebPreview)},
	}
	addThread(form, p.ThreadID)

	var msg Message
	err := c.call(ctx, "sendMessage", form, &msg)
	if err != nil && isEntityError(err) {
		// Retry without markup so the run continues.
		form.Set("parse_mode", "")
		var retry Message
		if err2 := c.call(ctx, "sendMessage", form, &retry); err2 == nil {
			return &retry, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *Client) sendPhoto(ctx context.Context, p Post, photo []byte, name string) (*Message, error) {
	if name == "" {
		name = "poster.jpg"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fields := map[string]string{
		"chat_id":                  p.ChatID,
		"caption":                  p.Caption,
		"parse_mode":               "HTML",
		"disable_notification":     boolStr(p.Silent),
		"disable_web_page_preview": boolStr(p.DisableWebPreview),
	}
	if p.ThreadID > 0 {
		fields["message_thread_id"] = strconv.Itoa(p.ThreadID)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename="%s"`, escapeQuotes(name)))
	h.Set("Content-Type", "image/jpeg")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(photo); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	var msg Message
	err = c.callMultipart(ctx, "sendPhoto", mw.FormDataContentType(), buf.Bytes(), &msg)
	if err != nil && isEntityError(err) {
		// Caption markup was invalid; send the photo without a parse mode.
		var buf2 bytes.Buffer
		mw2 := multipart.NewWriter(&buf2)
		fields["parse_mode"] = ""
		for k, v := range fields {
			if err := mw2.WriteField(k, v); err != nil {
				return nil, err
			}
		}
		part, err := mw2.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(photo); err != nil {
			return nil, err
		}
		if err := mw2.Close(); err != nil {
			return nil, err
		}
		var retry Message
		if err2 := c.callMultipart(ctx, "sendPhoto", mw2.FormDataContentType(), buf2.Bytes(), &retry); err2 == nil {
			return &retry, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// APIError is a structured Bot API failure.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: HTTP %d: %s", e.Code, e.Description)
}

func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", APIBase, c.Token, method)
}

func (c *Client) call(ctx context.Context, method string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method),
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, out)
}

func (c *Client) callMultipart(ctx context.Context, method, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return fmt.Errorf("telegram: unreadable response (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 160))
	}
	if !ar.OK {
		code := ar.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		desc := ar.Description
		if desc == "" {
			desc = "unknown error"
		}
		return &APIError{Code: code, Description: desc}
	}
	if out != nil && len(ar.Result) > 0 {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return fmt.Errorf("telegram: cannot decode result: %w", err)
		}
	}
	return nil
}

// isEntityError detects failures caused by malformed HTML in a caption.
func isEntityError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	d := strings.ToLower(ae.Description)
	return strings.Contains(d, "can't parse entities") ||
		strings.Contains(d, "unsupported start tag") ||
		strings.Contains(d, "unclosed") ||
		strings.Contains(d, "can't find end tag")
}

// IsBlocked reports whether the chat is unreachable because the bot was
// blocked or removed, which is not worth retrying.
func IsBlocked(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	d := strings.ToLower(ae.Description)
	return ae.Code == http.StatusForbidden ||
		strings.Contains(d, "bot was blocked") ||
		strings.Contains(d, "chat not found") ||
		strings.Contains(d, "kicked")
}

// IsRateLimit reports whether Telegram asked us to slow down.
func IsRateLimit(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == http.StatusTooManyRequests || strings.Contains(ae.Description, "Too Many Requests")
}

func addThread(form url.Values, id int) {
	if id > 0 {
		form.Set("message_thread_id", strconv.Itoa(id))
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func escapeQuotes(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
