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

// Chat is the subset of getChat needed to confirm a destination before
// publishing to it.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"` // private, group, supergroup, channel
	Title    string `json:"title"`
	Username string `json:"username"`
	// IsForum marks a supergroup whose topics accept message_thread_id.
	IsForum bool `json:"is_forum"`
}

// IsChannel reports whether the chat is a broadcast channel rather than a
// group, which changes how the bot has to be added.
func (ch Chat) IsChannel() bool { return ch.Type == "channel" }

// Display names the chat for an operator message, preferring the @username
// because that is what they typed into the settings form.
func (ch Chat) Display() string {
	name := ch.Title
	if name == "" {
		name = strconv.FormatInt(ch.ID, 10)
	}
	if ch.Username != "" {
		return fmt.Sprintf("%s (@%s)", name, ch.Username)
	}
	return name
}

// GetChat resolves a chat id, @username or numeric id.
func (c *Client) GetChat(ctx context.Context, chatID string) (*Chat, error) {
	var ch Chat
	if err := c.call(ctx, "getChat", url.Values{"chat_id": {chatID}}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ChatMember is the subset of getChatMember needed to tell whether the bot may
// post.
type ChatMember struct {
	Status          string `json:"status"` // creator, administrator, member, ...
	CanPostMessages bool   `json:"can_post_messages"`
}

func (m ChatMember) IsAdmin() bool {
	return m.Status == "creator" || m.Status == "administrator"
}

// CanPost reports whether the bot may publish to the chat.
//
// Telegram only sends can_post_messages on the administrator variant of the
// response; the owner variant has no such field, so a channel's creator would
// read as "false" and be refused even though it may obviously post. The flag
// is therefore consulted only for a plain administrator.
func (m ChatMember) CanPost(isChannel bool) bool {
	if !isChannel {
		// A group administrator may always send messages.
		return m.IsAdmin()
	}
	switch m.Status {
	case "creator":
		return true
	case "administrator":
		return m.CanPostMessages
	default:
		return false
	}
}

// GetChatMember returns one member's status. It is used to check the bot's own
// rights in the destination chat.
func (c *Client) GetChatMember(ctx context.Context, chatID string, userID int64) (*ChatMember, error) {
	var m ChatMember
	form := url.Values{
		"chat_id": {chatID},
		"user_id": {strconv.FormatInt(userID, 10)},
	}
	if err := c.call(ctx, "getChatMember", form, &m); err != nil {
		return nil, err
	}
	return &m, nil
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
	// CopyText, when set, adds a button that puts this text on the reader's
	// clipboard. It exists because Telegram refuses a magnet: URL as a
	// hyperlink -- as an entity it is rejected with "Wrong port number
	// specified in the URL", and in HTML the anchor is dropped silently -- so a
	// button is the only control that can hand a magnet over intact.
	//
	// Telegram caps the text at 256 characters (measured: 257 is rejected with
	// BUTTON_COPY_TEXT_INVALID), so the caller trims it to fit.
	CopyText string
}

// copyButtonLabel is the button's caption. It names the action rather than the
// payload, because the reader sees it before they see the link.
const copyButtonLabel = "复制磁力链接"

// copyTextButton is one inline keyboard button that copies text. The types are
// named rather than inline because Go cannot express a recursive anonymous
// struct: the rows and the buttons refer to each other.
type copyTextButton struct {
	Text     string       `json:"text"`
	CopyText copyTextBody `json:"copy_text"`
}

type copyTextBody struct {
	Text string `json:"text"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]copyTextButton `json:"inline_keyboard"`
}

// replyMarkup renders the inline keyboard, or an empty string when the post has
// no button. It is built as JSON because reply_markup is a JSON-serialised
// object in the Bot API, even for the URL-encoded endpoints.
func replyMarkup(p Post) string {
	if p.CopyText == "" {
		return ""
	}
	markup := inlineKeyboard{InlineKeyboard: [][]copyTextButton{{
		{Text: copyButtonLabel, CopyText: copyTextBody{Text: p.CopyText}},
	}}}
	b, err := json.Marshal(markup)
	if err != nil {
		// The struct is fixed, so this cannot happen; returning no button is
		// still better than publishing a broken one.
		return ""
	}
	return string(b)
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
	if markup := replyMarkup(p); markup != "" {
		form.Set("reply_markup", markup)
	}

	var msg Message
	err := c.call(ctx, "sendMessage", form, &msg)
	if err != nil {
		if isEntityError(err) {
			form.Set("parse_mode", "")
			var retry Message
			if err2 := c.call(ctx, "sendMessage", form, &retry); err2 == nil {
				return &retry, nil
			}
		}
		if isEntityError(err) || isCopyTextError(err) {
			form.Del("reply_markup")
			var retry Message
			if err2 := c.call(ctx, "sendMessage", form, &retry); err2 == nil {
				return &retry, nil
			}
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
	if markup := replyMarkup(p); markup != "" {
		fields["reply_markup"] = markup
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
	if err != nil {
		// Two parts of a photo post can be refused independently: the caption's
		// markup, and the button's text. Each is dropped in turn, and the
		// attempt that only has the harmless part removed is made first.
		if isEntityError(err) {
			fields["parse_mode"] = ""
			if retry, ok := c.resendPhoto(ctx, fields, h, photo); ok {
				return retry, nil
			}
		}
		if isEntityError(err) || isCopyTextError(err) {
			delete(fields, "reply_markup")
			if retry, ok := c.resendPhoto(ctx, fields, h, photo); ok {
				return retry, nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// resendPhoto rebuilds and resends a photo post with whatever fields it is
// given. It reports whether the send produced a message.
func (c *Client) resendPhoto(ctx context.Context, fields map[string]string,
	h textproto.MIMEHeader, photo []byte) (*Message, bool) {

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, false
		}
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, false
	}
	if _, err := part.Write(photo); err != nil {
		return nil, false
	}
	if err := mw.Close(); err != nil {
		return nil, false
	}
	var retry Message
	if err := c.callMultipart(ctx, "sendPhoto", mw.FormDataContentType(),
		buf.Bytes(), &retry); err != nil {
		return nil, false
	}
	return &retry, true
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

// isCopyTextError detects a button whose text the Bot API would not take, which
// happens when it exceeds the 256-character limit. The post is worth sending
// without the button rather than not at all.
func isCopyTextError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.Contains(strings.ToUpper(ae.Description), "BUTTON_COPY_TEXT_INVALID")
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
