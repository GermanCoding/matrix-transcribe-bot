package matrix

import (
	"testing"

	"maunium.net/go/mautrix/event"
)

func TestIsSupportedMediaMessage(t *testing.T) {
	if !isSupportedMediaMessage(&event.MessageEventContent{MsgType: event.MsgAudio}) {
		t.Fatal("expected audio message to be supported")
	}
	if isSupportedMediaMessage(&event.MessageEventContent{MsgType: event.MsgVideo}) {
		t.Fatal("expected video message to be ignored (only audio is supported)")
	}
	if isSupportedMediaMessage(&event.MessageEventContent{MsgType: event.MsgText}) {
		t.Fatal("expected text message to be ignored")
	}
}

func TestNormalizeTranscript(t *testing.T) {
	if got := normalizeTranscript(""); got != "No speech detected." {
		t.Fatalf("unexpected empty transcript normalization: %q", got)
	}
	if got := normalizeTranscript("  "); got != "No speech detected." {
		t.Fatalf("unexpected whitespace transcript normalization: %q", got)
	}
	if got := normalizeTranscript("hola"); got != "hola" {
		t.Fatalf("expected transcript to remain unchanged, got %q", got)
	}
}
