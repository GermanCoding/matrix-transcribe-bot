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

// authMode describes how the bot authenticates with the homeserver.
type authMode int

const (
	authPassword authMode = iota // Use username + password login
	authToken                    // Use a pre-existing access token
)

type Bot struct {
	cfg       *config.Config
	client    *mautrix.Client
	crypto    *cryptohelper.CryptoHelper
	bridge    *transcribe.Bridge
	startupMS int64
	auth      authMode
}

func NewBot(cfg *config.Config, bridge *transcribe.Bridge) (*Bot, error) {
	var (
		client *mautrix.Client
		err    error
		auth   authMode
	)

	if cfg.AccessToken != "" {
		// Token auth: credentials are provided directly; skip the LoginAs flow.
		auth = authToken
		client, err = mautrix.NewClient(cfg.Homeserver, id.UserID(cfg.UserID), cfg.AccessToken)
		if err != nil {
			return nil, err
		}
		if cfg.DeviceID != "" {
			client.DeviceID = id.DeviceID(cfg.DeviceID)
		} else {
			// Discover the device ID associated with this access token.
			resp, wErr := client.Whoami(context.Background())
			if wErr != nil {
				return nil, fmt.Errorf("whoami failed (is MATRIX_ACCESS_TOKEN valid?): %w", wErr)
			}
			client.DeviceID = resp.DeviceID
		}
	} else {
		// Password auth: delegate login and device-ID persistence to the crypto helper.
		auth = authPassword
		client, err = mautrix.NewClient(cfg.Homeserver, "", "")
		if err != nil {
			return nil, err
		}
	}

	helper, err := cryptohelper.NewCryptoHelper(client, cfg.PickleKey, cfg.CryptoDB())
	if err != nil {
		return nil, fmt.Errorf("init crypto helper: %w", err)
	}
	if auth == authPassword {
		// LoginAs lets the crypto helper handle login and device-ID persistence
		// so the same Olm identity (and its keys) is reused across restarts.
		helper.LoginAs = &mautrix.ReqLogin{
			Type:                     mautrix.AuthTypePassword,
			Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: cfg.UserID},
			Password:                 cfg.Password,
			InitialDeviceDisplayName: "matrix-transcribe-bot",
		}
	}
	client.Crypto = helper

	return &Bot{
		cfg:       cfg,
		client:    client,
		crypto:    helper,
		bridge:    bridge,
		startupMS: time.Now().UnixMilli(),
		auth:      auth,
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

	b.selfVerifyDevice(ctx)

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

// selfVerifyDevice checks whether this device is cross-signed (verified) and
// attempts to set up cross-signing + self-verify on first use when possible.
// Failures are logged and non-fatal — the bot still works in unverified rooms.
func (b *Bot) selfVerifyDevice(ctx context.Context) {
	mach := b.crypto.Machine()
	hasKeys, isVerified, err := mach.GetOwnVerificationStatus(ctx)
	if err != nil {
		log.Printf("Failed to check cross-signing status: %v", err)
		return
	}
	if isVerified {
		log.Printf("Device is cross-signed (verified)")
		return
	}
	if hasKeys {
		// Cross-signing exists on the account but this device is not yet signed.
		if b.cfg.RecoveryKey != "" {
			log.Printf("Cross-signing is set up but device is not verified; using recovery key to self-verify...")
			if err := mach.VerifyWithRecoveryKey(ctx, b.cfg.RecoveryKey); err != nil {
				log.Printf("Failed to self-verify with recovery key: %v (continuing without self-verification)", err)
				return
			}
			log.Printf("Device self-verified successfully using recovery key")
			return
		}
		// No recovery key provided — the user must verify via another client.
		log.Printf("Cross-signing is set up but this device is not self-verified; " +
			"set MATRIX_RECOVERY_KEY to self-verify automatically, or verify via another Matrix client")
		return
	}
	// No cross-signing keys at all — generate them and self-sign this device.
	log.Printf("No cross-signing keys found; setting up cross-signing...")
	if err := b.setupCrossSigning(ctx); err != nil {
		log.Printf("Failed to set up cross-signing: %v (continuing without self-verification)", err)
		return
	}
	log.Printf("Cross-signing set up; device is now self-verified")
}

// setupCrossSigning generates cross-signing keys, uploads them to the server,
// and self-signs this device so it appears verified.
// For password auth it supplies UIA via the stored password; for token auth it
// relies on the server accepting the request without interactive auth (which
// is possible on some homeservers and when using MAS / OAuth sessions).
func (b *Bot) setupCrossSigning(ctx context.Context) error {
	mach := b.crypto.Machine()

	var uiaCallback mautrix.UIACallback
	if b.auth == authPassword {
		uiaCallback = func(uiResp *mautrix.RespUserInteractive) interface{} {
			return &mautrix.ReqUIAuthLogin{
				BaseAuthData: mautrix.BaseAuthData{
					Type:    mautrix.AuthTypePassword,
					Session: uiResp.Session,
				},
				User:     b.client.UserID.String(),
				Password: b.cfg.Password,
			}
		}
	}

	if _, _, err := mach.GenerateAndUploadCrossSigningKeys(ctx, uiaCallback, ""); err != nil {
		return fmt.Errorf("generate and upload cross-signing keys: %w", err)
	}
	if err := mach.SignOwnDevice(ctx, mach.OwnIdentity()); err != nil {
		return fmt.Errorf("sign own device: %w", err)
	}
	if err := mach.SignOwnMasterKey(ctx); err != nil {
		return fmt.Errorf("sign own master key: %w", err)
	}
	return nil
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

	log.Printf("Received audio message %s in room %s", evt.ID, evt.RoomID)

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
	} else {
		log.Printf("Sent transcription reply for %s in room %s", evt.ID, evt.RoomID)
	}
}

func isSupportedMediaMessage(msg *event.MessageEventContent) bool {
	return msg.MsgType == event.MsgAudio
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
	log.Printf("Sent reaction %s to %s in room %s", key, eventID, roomID)
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
