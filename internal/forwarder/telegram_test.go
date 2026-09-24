package forwarder

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/telegram"
)

// botChat is what the fake Bot API reports for getChat.
type botChat struct {
	ID       int64
	Type     string
	Title    string
	Username string
	IsForum  bool
}

// fakeBot stands up a Bot API that answers the handshake methods, then either
// accepts or refuses the send. Each call is recorded so a test can assert that
// nothing was published when the handshake failed.
type fakeBot struct {
	me        TelegramIdentity
	chat      botChat
	chatErr   *apiErr
	member    memberStatus
	memberErr *apiErr
	sendErr   *apiErr
	sent      []map[string]string
}

type TelegramIdentity struct {
	ID       int64
	Username string
}

type memberStatus struct {
	Status      string
	CanPost     bool
	OmitCanPost bool
}

type apiErr struct {
	Code int
	Desc string
}

func (f *fakeBot) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")

		switch method {
		case "getMe":
			writeResult(w, map[string]any{
				"id": f.me.ID, "username": f.me.Username, "is_bot": true,
			})
		case "getChat":
			if f.chatErr != nil {
				writeError(w, f.chatErr)
				return
			}
			writeResult(w, map[string]any{
				"id": f.chat.ID, "type": f.chat.Type, "title": f.chat.Title,
				"username": f.chat.Username, "is_forum": f.chat.IsForum,
			})
		case "getChatMember":
			if f.memberErr != nil {
				writeError(w, f.memberErr)
				return
			}
			out := map[string]any{
				"user":   map[string]any{"id": f.me.ID, "is_bot": true},
				"status": f.member.Status,
			}
			// Telegram omits can_post_messages for non-administrators, so the
			// fake does too and the code must not read a missing field as true.
			if !f.member.OmitCanPost {
				out["can_post_messages"] = f.member.CanPost
			}
			writeResult(w, out)
		case "sendMessage":
			if f.sendErr != nil {
				writeError(w, f.sendErr)
				return
			}
			_ = r.ParseForm()
			rec := map[string]string{}
			for k := range r.Form {
				rec[k] = r.Form.Get(k)
			}
			f.sent = append(f.sent, rec)
			writeResult(w, map[string]any{"message_id": 1, "chat": map[string]any{"id": -1001}})
		default:
			writeError(w, &apiErr{Code: 404, Desc: "Not Found: method not found"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeResult(w http.ResponseWriter, result any) {
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, e *apiErr) {
	raw, _ := json.Marshal(map[string]any{
		"ok": false, "error_code": e.Code, "description": e.Desc,
	})
	_, _ = w.Write(raw)
}

// newChannelTestForwarder builds a forwarder whose settings point at a channel.
func newChannelTestForwarder(t *testing.T, chatID string) (*Forwarder, config.Settings) {
	t.Helper()
	dir := t.TempDir()
	cfgStore, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s := cfgStore.Get()
	s.BotToken = "1:test"
	s.ChatID = chatID
	if err := cfgStore.Update(s); err != nil {
		t.Fatalf("Update: %v", err)
	}
	state, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return New(cfgStore, state, log.New(io.Discard, "", 0)), cfgStore.Get()
}

// withFakeBot points the Telegram client at the fake API for one test.
func withFakeBot(t *testing.T, f *fakeBot) {
	t.Helper()
	srv := f.start(t)
	restore := telegram.APIBase
	telegram.APIBase = srv.URL
	t.Cleanup(func() { telegram.APIBase = restore })
}

// A broadcast channel the bot administers with the right to post is the
// intended configuration, so it must pass and actually deliver.
func TestTestTelegramAcceptsAdministeredChannel(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新", Username: "movie_updates"},
		member: memberStatus{Status: "administrator", CanPost: true},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "@movie_updates")

	msg, err := fwd.TestTelegram(context.Background(), settings)
	if err != nil {
		t.Fatalf("an administered channel should pass, got: %v", err)
	}
	if !strings.Contains(msg, "影视更新") {
		t.Errorf("the channel title should be reported, got %q", msg)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("expected exactly one test message, got %d", len(bot.sent))
	}
	if got := bot.sent[0]["chat_id"]; got != "@movie_updates" {
		t.Errorf("chat_id = %q, want the configured @username", got)
	}
}

// A bot that was added but not promoted is the most common mistake, and it
// needs a different fix from a wrong id, so the message must say so.
func TestTestTelegramRejectsNonAdminBot(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新"},
		member: memberStatus{Status: "member", CanPost: false},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "-1001234567890")

	_, err := fwd.TestTelegram(context.Background(), settings)
	if err == nil {
		t.Fatal("a non-admin bot must not be reported as working")
	}
	if !strings.Contains(err.Error(), "管理员") {
		t.Errorf("error should explain the missing admin role, got: %v", err)
	}
	if len(bot.sent) != 0 {
		t.Errorf("nothing should be published, got %d message(s)", len(bot.sent))
	}
}

// An administrator can be created without the publish permission in a channel,
// which looks configured but still cannot post.
func TestTestTelegramRejectsAdminWithoutPostRight(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新"},
		member: memberStatus{Status: "administrator", CanPost: false},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "-1001234567890")

	_, err := fwd.TestTelegram(context.Background(), settings)
	if err == nil {
		t.Fatal("an admin without the post right must not be reported as working")
	}
	if !strings.Contains(err.Error(), "发布消息") {
		t.Errorf("error should name the missing publish permission, got: %v", err)
	}
}

// A channel's owner is reported without can_post_messages, because Telegram
// only sends that field on the administrator variant. Reading the absent field
// as false would refuse a bot that can plainly post.
func TestTestTelegramAcceptsChannelOwnerWithoutPostField(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新"},
		member: memberStatus{Status: "creator", OmitCanPost: true},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "-1001234567890")

	if _, err := fwd.TestTelegram(context.Background(), settings); err != nil {
		t.Fatalf("a channel owner must pass even without can_post_messages: %v", err)
	}
	if len(bot.sent) != 1 {
		t.Errorf("expected the test message to be delivered, got %d", len(bot.sent))
	}
}

// A chat the bot cannot see reports the add-the-bot remediation rather than a
// bare Forbidden.
func TestTestTelegramExplainsUnreachableChat(t *testing.T) {
	bot := &fakeBot{
		me:      TelegramIdentity{ID: 999, Username: "extto_bot"},
		chatErr: &apiErr{Code: 400, Desc: "Bad Request: chat not found"},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "@nope")

	_, err := fwd.TestTelegram(context.Background(), settings)
	if err == nil {
		t.Fatal("an unreachable chat must fail")
	}
	for _, want := range []string{"加入该频道", "管理员"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// A group is a valid send target but not what a channel deployment wants, so
// the operator is told which one they configured.
func TestTestTelegramFlagsGroupInsteadOfChannel(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "supergroup", Title: "影迷群"},
		member: memberStatus{Status: "administrator", CanPost: true},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "-1001234567890")

	msg, err := fwd.TestTelegram(context.Background(), settings)
	if err != nil {
		t.Fatalf("a group is still deliverable: %v", err)
	}
	if !strings.Contains(msg, "不是频道") {
		t.Errorf("a group target should be called out, got %q", msg)
	}
}

// A channel has no forum topics, so a configured topic id must be rejected
// before it produces a confusing API error on every publish.
func TestTestTelegramRejectsTopicOnChannel(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新"},
		member: memberStatus{Status: "administrator", CanPost: true},
	}
	withFakeBot(t, bot)
	fwd, settings := newChannelTestForwarder(t, "-1001234567890")
	settings.MessageTopic = "42"

	_, err := fwd.TestTelegram(context.Background(), settings)
	if err == nil {
		t.Fatal("a topic id on a channel must be rejected")
	}
	if !strings.Contains(err.Error(), "论坛话题") {
		t.Errorf("error should name the forum-topic setting, got: %v", err)
	}
}

// An invalid token fails before anything else is attempted.
func TestTestTelegramRejectsBadToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiErr{Code: 401, Desc: "Unauthorized"})
	}))
	defer srv.Close()
	restore := telegram.APIBase
	telegram.APIBase = srv.URL
	defer func() { telegram.APIBase = restore }()

	fwd, settings := newChannelTestForwarder(t, "-1001234567890")
	if _, err := fwd.TestTelegram(context.Background(), settings); err == nil {
		t.Fatal("a bad token must fail")
	}
}

// Operators copy whatever Telegram shows them, so the accepted forms have to
// cover links, bare usernames and private-channel links.
func TestNormalizeChatRef(t *testing.T) {
	cases := map[string]string{
		"@movie_updates":                "@movie_updates",
		"movie_updates":                 "@movie_updates",
		"https://t.me/movie_updates":    "@movie_updates",
		"http://t.me/movie_updates":     "@movie_updates",
		"t.me/movie_updates":            "@movie_updates",
		"https://t.me/movie_updates/42": "@movie_updates",
		"-1001234567890":                "-1001234567890",
		"https://t.me/c/1234567890":     "-1001234567890",
		"https://t.me/c/1234567890/45":  "-1001234567890",
		"  @movie_updates  ":            "@movie_updates",
		"":                              "",
		"https://t.me/c/notanumber/45":  "@c",
	}
	for in, want := range cases {
		if got := normalizeChatRef(in); got != want {
			t.Errorf("normalizeChatRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// LookupChat reports the resolved destination and the bot's rights there, which
// is what the panel shows before the operator saves an id.
func TestLookupChatReportsRights(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新", Username: "movie_updates"},
		member: memberStatus{Status: "administrator", CanPost: true},
	}
	withFakeBot(t, bot)
	fwd, _ := newChannelTestForwarder(t, "-1001234567890")

	chat, member, err := fwd.LookupChat(context.Background(), "https://t.me/movie_updates", "")
	if err != nil {
		t.Fatalf("LookupChat: %v", err)
	}
	if chat.ID != -1001234567890 || chat.Type != "channel" {
		t.Errorf("unexpected chat: %+v", chat)
	}
	if chat.Display() != "影视更新 (@movie_updates)" {
		t.Errorf("Display() = %q", chat.Display())
	}
	if !member.IsAdmin() || !member.CanPost(true) {
		t.Errorf("member should be a posting admin: %+v", member)
	}
}

// The lookup must fail loudly when the token is missing, rather than silently
// reporting an unusable channel. The empty reference is checked first so this
// does not reach the network.
func TestLookupChatRequiresTokenAndReference(t *testing.T) {
	fwd, _ := newChannelTestForwarder(t, "-1001234567890")

	if _, _, err := fwd.LookupChat(context.Background(), "   ", ""); err == nil {
		t.Error("an empty reference must be rejected")
	}

	// Empty out the token to exercise that guard.
	dir := t.TempDir()
	cfgStore, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s := cfgStore.Get()
	s.BotToken = ""
	if err := cfgStore.Update(s); err != nil {
		t.Fatalf("Update: %v", err)
	}
	state, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	bare := New(cfgStore, state, log.New(io.Discard, "", 0))

	if _, _, err := bare.LookupChat(context.Background(), "@movie_updates", ""); err == nil {
		t.Error("a missing token must be reported before any API call")
	}
}

// The token typed into the form has to be used before it is saved, exactly like
// the Telegram test, otherwise a first-time setup cannot check its channel at
// all. A stored-but-empty token plus an on-screen token must therefore not hit
// the "no token" guard.
func TestLookupChatPrefersOnScreenToken(t *testing.T) {
	bot := &fakeBot{
		me:     TelegramIdentity{ID: 999, Username: "extto_bot"},
		chat:   botChat{ID: -1001234567890, Type: "channel", Title: "影视更新"},
		member: memberStatus{Status: "administrator", CanPost: true},
	}
	withFakeBot(t, bot)

	dir := t.TempDir()
	cfgStore, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	state, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// No stored token: only the on-screen one can satisfy the call.
	unsaved := New(cfgStore, state, log.New(io.Discard, "", 0))

	chat, member, err := unsaved.LookupChat(context.Background(), "@movie_updates", "1:on-screen")
	if err != nil {
		t.Fatalf("the on-screen token should be used, got: %v", err)
	}
	if chat.ID != -1001234567890 {
		t.Errorf("chat = %+v, want the fake channel", chat)
	}
	if !member.CanPost(true) {
		t.Errorf("member = %+v, want a posting admin", member)
	}
}
