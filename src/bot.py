import asyncio
import json
import logging
import os
import tempfile
import time

from mautrix.client import Client
from mautrix.client.state_store import MemoryStateStore
from mautrix.crypto.attachments import decrypt_attachment
from mautrix.errors import MatrixError
from mautrix.types import (
    EventType,
    Membership,
    MessageEvent,
    MessageType,
    StateEvent,
    TextMessageEventContent,
    RelatesTo,
    InReplyTo,
)
from mautrix.types.primitive import EventID, RoomID

logger = logging.getLogger(__name__)


class TranscriptBot:
    def __init__(
        self,
        homeserver: str,
        user_id: str,
        password: str,
        store_path: str,
        transcriber,
    ):
        self.password = password
        self.store_path = store_path
        self.transcriber = transcriber
        self._startup_ms = int(time.time() * 1000)
        self._session_file = os.path.join(store_path, "session.json")

        self.client = Client(
            mxid=user_id,
            base_url=homeserver,
            state_store=MemoryStateStore(),
        )

    async def start(self):
        if os.path.exists(self._session_file):
            await self._restore_session()
        else:
            await self._fresh_login()

        await self._setup_crypto()

        logger.info(
            "Logged in as %s (device %s)", self.client.mxid, self.client.device_id
        )

        self.client.add_event_handler(EventType.ROOM_MESSAGE, self.on_audio_message)
        self.client.add_event_handler(EventType.ROOM_MEMBER, self._on_invite)

        await self.client.start(None)

    async def _fresh_login(self):
        response = await self.client.login(password=self.password)

        os.makedirs(self.store_path, exist_ok=True)
        with open(self._session_file, "w") as f:
            json.dump(
                {
                    "access_token": response.access_token,
                    "device_id": response.device_id,
                    "user_id": response.user_id,
                },
                f,
            )
        logger.info("New session saved (device %s)", response.device_id)

    async def _restore_session(self):
        with open(self._session_file) as f:
            session = json.load(f)

        self.client.api.token = session["access_token"]
        self.client.device_id = session["device_id"]
        self.client.mxid = session["user_id"]
        logger.info("Restored session (device %s)", session["device_id"])

    async def _setup_crypto(self):
        try:
            from mautrix.crypto import OlmMachine
            # PgCryptoStore/PgCryptoStateStore support both PostgreSQL and SQLite via
            # mautrix's async_db abstraction; here we use a local SQLite file.
            from mautrix.crypto.store.asyncpg import PgCryptoStore, PgCryptoStateStore
            from mautrix.types import TrustState
            from mautrix.util.async_db import Database

            os.makedirs(self.store_path, exist_ok=True)
            db_path = os.path.join(self.store_path, "crypto.db")
            db = Database.create(
                f"sqlite:///{db_path}",
                upgrade_table=PgCryptoStore.upgrade_table,
            )
            await db.start()

            # account_id scopes the store to this bot's identity; pickle_key encrypts
            # the serialised Olm account on disk and must remain stable across restarts.
            crypto_store = PgCryptoStore(str(self.client.mxid), "mxbot", db)
            state_store = PgCryptoStateStore(db)
            await crypto_store.open()

            self.client.state_store = state_store

            machine = OlmMachine(self.client, crypto_store, state_store)
            # Allow sending to all devices without prior explicit verification (TOFU).
            machine.send_keys_min_trust = TrustState.UNVERIFIED
            await machine.load()

            self.client.crypto = machine
            logger.info("E2E encryption enabled")
        except ImportError:
            logger.warning("E2E crypto dependencies not available, running without encryption")
        except Exception:
            logger.exception("Failed to set up E2E crypto, continuing without encryption")

    async def stop(self):
        # client.stop() is synchronous — it cancels the syncing task.
        self.client.stop()
        await self.client.api.session.close()

    async def _on_invite(self, event: StateEvent):
        if (
            event.content.membership == Membership.INVITE
            and event.state_key == self.client.mxid
        ):
            try:
                await self.client.join_room_by_id(event.room_id)
                logger.info("Joined room %s", event.room_id)
            except MatrixError as e:
                logger.error("Failed to join %s: %s", event.room_id, e)

    async def on_audio_message(self, event: MessageEvent):
        if not hasattr(event.content, "msgtype") or event.content.msgtype != MessageType.AUDIO:
            return

        # Ignore own messages
        if event.sender == self.client.mxid:
            return

        # Ignore messages from before startup
        if event.timestamp < self._startup_ms:
            return

        logger.info(
            "Audio from %s in %s (%s)",
            event.sender,
            event.room_id,
            event.event_id,
        )

        # React with robot emoji
        reaction_event_id = await self._react(event.room_id, event.event_id, "\U0001f916")

        try:
            audio_path = await self._download_media(event)
            if not audio_path:
                await self._remove_reaction(event.room_id, reaction_event_id)
                return

            try:
                loop = asyncio.get_event_loop()
                start = time.monotonic()
                text = await loop.run_in_executor(
                    None, self.transcriber.transcribe, audio_path
                )
                elapsed = time.monotonic() - start
                logger.info(
                    "Transcribed %s in %.1fs", event.event_id, elapsed
                )
            finally:
                os.unlink(audio_path)

            await self._remove_reaction(event.room_id, reaction_event_id)

            if not text or not text.strip():
                await self._reply(event.room_id, event.event_id, "No speech detected.")
            else:
                await self._reply(event.room_id, event.event_id, text)

        except Exception:
            logger.exception("Transcription failed for %s", event.event_id)
            await self._remove_reaction(event.room_id, reaction_event_id)
            await self._react(event.room_id, event.event_id, "\u274c")

    async def _download_media(self, event: MessageEvent):
        content = event.content

        if content.file:
            mxc = content.file.url
        else:
            mxc = content.url

        try:
            data = await self.client.download_media(mxc)
        except Exception as e:
            logger.error("Failed to download media: %s", e)
            return None

        if content.file:
            data = decrypt_attachment(
                data,
                content.file.key.key,
                content.file.hashes["sha256"],
                content.file.iv,
            )

        body = content.body or "audio.ogg"
        suffix = os.path.splitext(body)[1] or ".ogg"
        tmp = tempfile.NamedTemporaryFile(delete=False, suffix=suffix)
        tmp.write(data)
        tmp.close()
        return tmp.name

    async def _react(self, room_id: RoomID, event_id: EventID, emoji: str):
        try:
            return await self.client.react(room_id, event_id, emoji)
        except MatrixError as e:
            logger.warning("Failed to send reaction: %s", e)
            return None

    async def _remove_reaction(self, room_id: RoomID, reaction_event_id: EventID):
        if reaction_event_id:
            try:
                await self.client.redact(room_id, reaction_event_id)
            except MatrixError as e:
                logger.warning("Failed to redact reaction: %s", e)

    async def _reply(self, room_id: RoomID, event_id: EventID, text: str):
        content = TextMessageEventContent(
            msgtype=MessageType.TEXT,
            body=text,
            relates_to=RelatesTo(
                in_reply_to=InReplyTo(event_id=event_id),
            ),
        )
        await self.client.send_message_event(room_id, EventType.ROOM_MESSAGE, content)

