import sys
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from mautrix.types import EventType, MessageType


@pytest.fixture
def mock_transcriber():
    t = MagicMock()
    t.transcribe.return_value = "Hola mundo"
    return t


@pytest.fixture
def bot(mock_transcriber):
    with patch("src.bot.Client"), patch("src.bot.MemoryStateStore"):
        from src.bot import TranscriptBot

        b = TranscriptBot(
            homeserver="https://matrix.example.com",
            user_id="@bot:example.com",
            password="password",
            store_path="/tmp/store",
            transcriber=mock_transcriber,
        )
        b.client = AsyncMock()
        b.client.mxid = "@bot:example.com"
        return b


def make_audio_event(
    sender,
    event_id="$event1",
    timestamp=None,
    url="mxc://example.com/audio",
    room_id="!room:example.com",
):
    event = MagicMock()
    event.sender = sender
    event.event_id = event_id
    event.timestamp = timestamp or 9999999999999
    event.room_id = room_id
    event.content = MagicMock()
    event.content.msgtype = MessageType.AUDIO
    event.content.file = None
    event.content.url = url
    event.content.body = "voice-message.ogg"
    return event


class TestEventFiltering:
    @pytest.mark.asyncio
    async def test_ignores_own_messages(self, bot):
        event = make_audio_event(sender="@bot:example.com")

        await bot.on_audio_message(event)

        bot.client.react.assert_not_called()

    @pytest.mark.asyncio
    async def test_ignores_old_messages(self, bot):
        bot._startup_ms = 5000
        event = make_audio_event(sender="@user:example.com", timestamp=1000)

        await bot.on_audio_message(event)

        bot.client.react.assert_not_called()


class TestReactions:
    @pytest.mark.asyncio
    async def test_reacts_with_robot_on_start(self, bot):
        bot._startup_ms = 0
        event = make_audio_event(sender="@user:example.com")

        bot.client.react.return_value = "$reaction1"
        bot.client.download_media.return_value = b"fake audio"

        await bot.on_audio_message(event)

        # react should have been called with the robot emoji first
        first_call = bot.client.react.call_args_list[0]
        assert first_call[0][0] == "!room:example.com"
        assert first_call[0][1] == "$event1"
        assert first_call[0][2] == "\U0001f916"

    @pytest.mark.asyncio
    async def test_removes_robot_reaction_on_success(self, bot):
        bot._startup_ms = 0
        event = make_audio_event(sender="@user:example.com")

        bot.client.react.return_value = "$reaction1"
        bot.client.download_media.return_value = b"fake audio"

        await bot.on_audio_message(event)

        bot.client.redact.assert_called_once_with("!room:example.com", "$reaction1")


class TestReplyFormat:
    @pytest.mark.asyncio
    async def test_reply_contains_transcribed_text(self, bot):
        bot._startup_ms = 0
        event = make_audio_event(sender="@user:example.com")

        bot.client.react.return_value = "$reaction1"
        bot.client.download_media.return_value = b"fake audio"

        await bot.on_audio_message(event)

        bot.client.send_message_event.assert_called_once()
        call = bot.client.send_message_event.call_args
        assert call[0][0] == "!room:example.com"
        assert call[0][1] == EventType.ROOM_MESSAGE
        content = call[0][2]
        assert content.body == "Hola mundo"
        assert content.relates_to.in_reply_to.event_id == "$event1"

    @pytest.mark.asyncio
    async def test_replies_no_speech_for_empty_result(self, bot, mock_transcriber):
        bot._startup_ms = 0
        mock_transcriber.transcribe.return_value = ""
        event = make_audio_event(sender="@user:example.com")

        bot.client.react.return_value = "$reaction1"
        bot.client.download_media.return_value = b"fake audio"

        await bot.on_audio_message(event)

        bot.client.send_message_event.assert_called_once()
        call = bot.client.send_message_event.call_args
        assert call[0][2].body == "No speech detected."


class TestErrorHandling:
    @pytest.mark.asyncio
    async def test_reacts_with_x_on_transcription_failure(self, bot, mock_transcriber):
        bot._startup_ms = 0
        mock_transcriber.transcribe.side_effect = RuntimeError("model error")
        event = make_audio_event(sender="@user:example.com")

        bot.client.react.return_value = "$reaction1"
        bot.client.download_media.return_value = b"fake audio"

        await bot.on_audio_message(event)

        # Should redact robot reaction
        bot.client.redact.assert_called()
        # Should send X reaction
        x_calls = [
            c for c in bot.client.react.call_args_list
            if c[0][2] == "\u274c"
        ]
        assert len(x_calls) == 1

