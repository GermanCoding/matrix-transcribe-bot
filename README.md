# matrix-transcript-bot

A self-hosted Matrix bot that transcribes audio messages into text.

The Matrix integration is implemented in Go using [`mautrix`](https://github.com/mautrix/go), with full E2EE (end-to-end encryption) support. Transcription is handled by Python [`faster-whisper`](https://github.com/SYSTRAN/faster-whisper).

## How it works

1. A user sends `m.audio` or `m.video` in a room where the bot is present.
2. The bot reacts with 🤖 while processing.
3. The Go bot downloads and (if needed) decrypts the media from Matrix.
4. The bot asks the Python bridge to transcribe with `faster-whisper`.
5. On success, the bot removes 🤖 and replies with text.
6. On failure, the bot removes 🤖 and reacts with ❌.

## E2EE

E2EE is enabled out of the box via `mautrix` and its `cryptohelper` package:

- The bot's Olm identity and Megolm session keys are stored in a SQLite database (`data/store/crypto.db`) encrypted with `PICKLE_KEY`.
- The bot uses the same device identity across restarts (device ID is persisted in the crypto store).
- It trusts all non-blacklisted devices (TOFU) so it can share encryption session keys with everyone in a room.
- Encrypted file attachments are automatically decrypted before transcription.

### Cross-signing / verification

On startup the bot checks whether the account already has cross-signing keys:

- **No cross-signing keys**: The bot generates a new master key, self-signing key, and user-signing key, uploads them to the homeserver, and self-signs its own device so it shows as *verified* in other clients. For password-based login the upload is authenticated automatically; for token-based login it succeeds if the homeserver accepts the request without interactive auth (e.g. on MAS / OAuth2 sessions). If the upload fails the bot logs a warning and continues operating in an unverified state.
- **Cross-signing already set up, device not yet verified**: The bot logs a message and continues. Verify it manually from another client, or start fresh with a new device.
- **Cross-signing already set up, device verified**: Nothing to do.

**Important:** `PICKLE_KEY` must be set before the first run and never changed afterwards. Changing it will corrupt the crypto store.

## Setup

### 1. Create a bot account

Create a Matrix account for the bot on your homeserver.

### 2. Configure

```bash
cp .env.example .env
```

Generate a pickle key:

```bash
openssl rand -hex 32
```

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MATRIX_HOMESERVER` | (required) | Homeserver URL |
| `MATRIX_USER_ID` | (required) | Bot user ID |
| `MATRIX_PASSWORD` | (required if no token) | Bot account password |
| `MATRIX_ACCESS_TOKEN` | (required if no password) | Pre-existing access token (for SSO / MAS / device-code flows) |
| `MATRIX_DEVICE_ID` | (auto via `/whoami`) | Device ID for the access token; auto-detected if not set |
| `PICKLE_KEY` | (required) | Secret key for E2EE store encryption — generate once and keep stable |
| `STORE_PATH` | `/app/store` | Directory for session and E2EE key storage |
| `WHISPER_MODEL` | `large-v3` | faster-whisper model name |
| `WHISPER_LANGUAGE` | `es` | Transcription language code |
| `WHISPER_MODEL_DIR` | `/app/models` | Whisper model cache directory |
| `WHISPER_CPU_THREADS` | `0` | CPU threads for transcription (0 = all cores) |
| `PYTHON_BIN` | `python` | Python interpreter for the transcription bridge |

#### Login flows

**Password login** (default): set `MATRIX_PASSWORD`.

**Token login** (SSO / MAS / device-code): obtain an access token through any
out-of-band flow (e.g. a browser SSO session or device-code grant), then set
`MATRIX_ACCESS_TOKEN`. Leave `MATRIX_PASSWORD` unset. Optionally pin the token
to a specific device by also setting `MATRIX_DEVICE_ID`; otherwise the bot
auto-discovers the device via `/whoami`.

> **Note:** The crypto store is tied to a single device ID. If you rotate the
> access token to a *different* device you must delete `STORE_PATH/crypto.db`
> so a fresh Olm identity is created for the new device.

### 3. Run

```bash
docker compose up -d
```

The bot will download the whisper model on first run (~3 GB, cached in `data/models/`).

## Data persistence

- `data/store/` — Matrix session and E2EE key storage (`crypto.db`). **Do not delete.**
- `data/models/` — Whisper model cache (can be deleted; re-downloads on next start).
