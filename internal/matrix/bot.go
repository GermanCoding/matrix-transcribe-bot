package matrix

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GermanCoding/matrix-transcribe-bot/internal/config"
	"github.com/GermanCoding/matrix-transcribe-bot/internal/transcribe"
	_ "go.mau.fi/util/dbutil/litestream" // SQLite driver required for E2EE store
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Bot struct {
	cfg       *config.Config
	client    *mautrix.Client
	crypto    *cryptohelper.CryptoHelper
	bridge    *transcribe.Bridge
	startupMS int64
}

func NewBot(cfg *config.Config, bridge *transcribe.Bridge) (*Bot, error) {
	client, err := mautrix.NewClient(cfg.Homeserver, "", "")
	if err != nil {
		return nil, err
	}

	helper, err := cryptohelper.NewCryptoHelper(client, cfg.PickleKey, cfg.CryptoDB())
	if err != nil {
		return nil, fmt.Errorf("init crypto helper: %w", err)
	}
	// LoginAs lets the crypto helper handle login and device-ID persistence
	// so the same Olm identity (and its keys) is reused across restarts.
	helper.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: cfg.UserID},
		Password:                 cfg.Password,
		InitialDeviceDisplayName: "matrix-transcribe-bot",
	}
	client.Crypto = helper

	return &Bot{
		cfg:       cfg,
		client:    client,
		crypto:    helper,
		bridge:    bridge,
		startupMS: time.Now().UnixMilli(),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	// Init initialises the Olm account (creating it on first run), shares our
	// device keys, and registers all crypto-related sync handlers.
	if err := b.crypto.Init(ctx); err != nil {
		return fmt.Errorf("crypto init: %w", err)
	}
	defer b.crypto.Close()

	// TOFU: share encryption sessions with all non-blacklisted devices so that
	// members of encrypted rooms can receive the bot's replies.
	b.crypto.Machine().ShareKeysMinTrust = id.TrustStateUnset

	log.Printf("Logged in as %s (device %s)", b.client.UserID, b.client.DeviceID)
	b.registerHandlers()

	errCh := make(chan error, 1)
	go func() {
		errCh <- b.client.Sync()
	}()

	select {
	case <-ctx.Done():
		b.client.StopSync()
		return nil
	case err := <-errCh:
		if err == context.Canceled {
			return nil
		}
		return err
	}
}

func (b *Bot) registerHandlers() {
	syncer := b.client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.StateMember, b.onMemberEvent)
	// EventMessage handles unencrypted rooms.
	// For encrypted rooms the cryptohelper decrypts EventEncrypted events and
	// re-dispatches them as EventMessage with SourceDecrypted set, so the same
	// handler covers both code paths automatically.
	syncer.OnEventType(event.EventMessage, b.onMessageEvent)
}

func (b *Bot) onMemberEvent(ctx context.Context, evt *event.Event) {
	if evt.GetStateKey() != b.client.UserID.String() {
		return
	}
	member := evt.Content.AsMember()
	if member.Membership != event.MembershipInvite {
		return
	}
	if _, err := b.client.JoinRoomByID(context.Background(), evt.RoomID); err != nil {
		log.Printf("Failed joining %s: %v", evt.RoomID, err)
		return
	}
	log.Printf("Joined room %s", evt.RoomID)
}

func (b *Bot) onMessageEvent(ctx context.Context, evt *event.Event) {
	if evt.Sender == b.client.UserID {
		return
	}
	if evt.Timestamp < b.startupMS {
		return
	}

	msg := evt.Content.AsMessage()
	if !isSupportedMediaMessage(msg) {
		return
	}

	reactionID := b.react(evt.RoomID, evt.ID, "🤖")

	path, err := b.downloadAudio(ctx, msg)
	if err != nil {
		log.Printf("download failed for %s: %v", evt.ID, err)
		b.removeReaction(evt.RoomID, reactionID)
		return
	}
	defer os.Remove(path)

	text, err := b.bridge.Transcribe(path)
	if err != nil {
		log.Printf("transcription failed for %s: %v", evt.ID, err)
		b.removeReaction(evt.RoomID, reactionID)
		b.react(evt.RoomID, evt.ID, "❌")
		return
	}

	b.removeReaction(evt.RoomID, reactionID)
	text = normalizeTranscript(text)
	if err := b.reply(evt.RoomID, evt.ID, text); err != nil {
		log.Printf("reply failed for %s: %v", evt.ID, err)
	}
}

func isSupportedMediaMessage(msg *event.MessageEventContent) bool {
	return msg.MsgType == event.MsgAudio || msg.MsgType == event.MsgVideo
}

func normalizeTranscript(text string) string {
	if strings.TrimSpace(text) == "" {
		return "No speech detected."
	}
	return text
}

func (b *Bot) downloadAudio(ctx context.Context, msg *event.MessageEventContent) (string, error) {
	mxc := msg.URL
	if msg.File != nil && msg.File.URL != "" {
		mxc = msg.File.URL
	}
	if mxc == "" {
		return "", fmt.Errorf("missing media URL")
	}

	parsedMXC, err := mxc.Parse()
	if err != nil {
		return "", err
	}

	data, err := b.client.DownloadBytes(context.Background(), parsedMXC)
	if err != nil {
		return "", err
	}

	// Decrypt attachment payload for messages from encrypted rooms.
	if msg.File != nil {
		if err := msg.File.DecryptInPlace(data); err != nil {
			return "", fmt.Errorf("decrypt attachment: %w", err)
		}
	}

	suffix := filepath.Ext(msg.Body)
	if suffix == "" {
		suffix = ".ogg"
	}
	tmp, err := os.CreateTemp("", "matrix-audio-*"+suffix)
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := tmp.Write(data); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func (b *Bot) react(roomID id.RoomID, eventID id.EventID, key string) id.EventID {
	resp, err := b.client.SendMessageEvent(context.Background(), roomID, event.EventReaction, map[string]any{
		"m.relates_to": map[string]any{
			"rel_type": "m.annotation",
			"event_id": eventID,
			"key":      key,
		},
	})
	if err != nil {
		log.Printf("reaction failed: %v", err)
		return ""
	}
	return resp.EventID
}

func (b *Bot) removeReaction(roomID id.RoomID, reactionID id.EventID) {
	if reactionID == "" {
		return
	}
	_, err := b.client.RedactEvent(context.Background(), roomID, reactionID, mautrix.ReqRedact{})
	if err != nil {
		log.Printf("failed to redact reaction: %v", err)
	}
}

func (b *Bot) reply(roomID id.RoomID, eventID id.EventID, text string) error {
	content := event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	content.SetReply(&event.Event{ID: eventID})
	_, err := b.client.SendMessageEvent(context.Background(), roomID, event.EventMessage, &content)
	return err
}
