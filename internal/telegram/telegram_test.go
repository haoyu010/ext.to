package telegram

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capturedPost records one request the mock Bot API received.
type capturedPost struct {
	method string
	fields map[string]string
}

// newMockBot starts a server that records what was sent and refuses any request
// carrying a copy button, imitating the Bot API's response to text over the
// 256-character ceiling.
func newMockBot(t *testing.T, rejectButton bool) (*httptest.Server, *[]capturedPost) {
	t.Helper()
	var got []capturedPost
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := capturedPost{
			method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:],
			fields: map[string]string{},
		}
		if ctype := r.Header.Get("Content-Type"); strings.HasPrefix(ctype, "multipart/form-data") {
			_, params, err := mime.ParseMediaType(ctype)
			if err != nil {
				t.Errorf("parse content type: %v", err)
				return
			}
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("read multipart: %v", err)
					return
				}
				b, _ := io.ReadAll(part)
				if part.FileName() == "" {
					rec.fields[part.FormName()] = string(b)
				}
			}
		} else {
			_ = r.ParseForm()
			for k := range r.Form {
				rec.fields[k] = r.Form.Get(k)
			}
		}
		got = append(got, rec)

		w.Header().Set("Content-Type", "application/json")
		if rejectButton && rec.fields["reply_markup"] != "" {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,` +
				`"description":"Bad Request: BUTTON_COPY_TEXT_INVALID"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":-100123}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// A button the Bot API will not take must not cost the post. The caption is what
// the reader came for, and the magnet is printed in it, so the button is dropped
// and the message is sent anyway.
func TestSendDropsAButtonTheAPIRefuses(t *testing.T) {
	srv, sent := newMockBot(t, true)
	restore := APIBase
	APIBase = srv.URL
	defer func() { APIBase = restore }()

	msg, err := New("1:test").Send(context.Background(), Post{
		ChatID:   "-100123",
		Caption:  "直达链接：<code>magnet:?xt=urn:btih:DEADBEEF</code>",
		CopyText: "magnet:?xt=urn:btih:DEADBEEF",
	}, nil, "")
	if err != nil {
		t.Fatalf("a rejected button lost the post: %v", err)
	}
	if msg == nil || msg.MessageID != 1 {
		t.Fatalf("no message was returned: %+v", msg)
	}
	if len(*sent) < 2 {
		t.Fatalf("expected a retry without the button, got %d request(s)", len(*sent))
	}
	last := (*sent)[len(*sent)-1]
	if last.fields["reply_markup"] != "" {
		t.Errorf("the retry still carried a button: %q", last.fields["reply_markup"])
	}
	if last.fields["text"] == "" {
		t.Error("the retry dropped the message text as well as the button")
	}
	if last.fields["parse_mode"] != "HTML" {
		t.Errorf("the retry should keep the markup, got parse_mode=%q", last.fields["parse_mode"])
	}
}

// The same, for the photo path, which is the one a poster post takes.
func TestSendPhotoDropsAButtonTheAPIRefuses(t *testing.T) {
	srv, sent := newMockBot(t, true)
	restore := APIBase
	APIBase = srv.URL
	defer func() { APIBase = restore }()

	msg, err := New("1:test").Send(context.Background(), Post{
		ChatID:   "-100123",
		Caption:  "直达链接：<code>magnet:?xt=urn:btih:DEADBEEF</code>",
		CopyText: "magnet:?xt=urn:btih:DEADBEEF",
	}, []byte("jpeg bytes"), "poster.jpg")
	if err != nil {
		t.Fatalf("a rejected button lost the post: %v", err)
	}
	if msg == nil {
		t.Fatal("no message was returned")
	}
	if len(*sent) < 2 {
		t.Fatalf("expected a retry without the button, got %d request(s)", len(*sent))
	}
	last := (*sent)[len(*sent)-1]
	if last.method != "sendPhoto" {
		t.Errorf("the retry changed method to %s", last.method)
	}
	if last.fields["reply_markup"] != "" {
		t.Errorf("the retry still carried a button: %q", last.fields["reply_markup"])
	}
	if last.fields["caption"] == "" {
		t.Error("the retry dropped the caption")
	}
}

// The copy button is the only control that can hand a magnet over: Telegram
// refuses a magnet: URL as a hyperlink, as an entity ("Wrong port number
// specified in the URL") and in HTML (the anchor is dropped), so the shape of
// what goes on the wire is worth pinning down.
func TestReplyMarkupCarriesTheCopyButton(t *testing.T) {
	got := replyMarkup(Post{CopyText: "magnet:?xt=urn:btih:DEADBEEF"})
	if got == "" {
		t.Fatal("a post with copy text was sent without a button")
	}
	var decoded struct {
		InlineKeyboard [][]struct {
			Text     string `json:"text"`
			CopyText struct {
				Text string `json:"text"`
			} `json:"copy_text"`
		} `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("the markup is not valid JSON: %v (%s)", err, got)
	}
	if len(decoded.InlineKeyboard) != 1 || len(decoded.InlineKeyboard[0]) != 1 {
		t.Fatalf("the markup is not one button: %s", got)
	}
	b := decoded.InlineKeyboard[0][0]
	if bCopy := b.CopyText.Text; bCopy != "magnet:?xt=urn:btih:DEADBEEF" {
		t.Errorf("the button copies %q, want the magnet", bCopy)
	}
	if b.Text == "" {
		t.Error("the button has no label")
	}
	// The label is what the reader sees before the payload, so it names the
	// action rather than the link.
	if strings.Contains(b.Text, "magnet:") {
		t.Errorf("the button label shows the link instead of naming the action: %q", b.Text)
	}

	// A post with no magnet has no button: an empty one would be a control that
	// copies nothing.
	if got := replyMarkup(Post{}); got != "" {
		t.Errorf("a post without copy text was given a button: %q", got)
	}
}

// A button whose text the Bot API refuses must not cost the whole post. The
// failure is recognised by name so the caller can drop the button and send the
// caption, which is the part the reader came for.
func TestIsCopyTextErrorRecognisesTheButtonFailure(t *testing.T) {
	if !isCopyTextError(&APIError{Code: 400, Description: "Bad Request: BUTTON_COPY_TEXT_INVALID"}) {
		t.Error("the button failure was not recognised")
	}
	for _, other := range []error{
		&APIError{Code: 400, Description: "Bad Request: can't parse entities: Unclosed start tag"},
		&APIError{Code: 400, Description: "Bad Request: message caption is too long"},
		&APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"},
		nil,
	} {
		if isCopyTextError(other) {
			t.Errorf("%v was mistaken for a copy-button failure", other)
		}
	}
}
