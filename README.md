# matrix-transcript-bot

A self-hosted Matrix bot that transcribes audio messages into text.

The Matrix integration is implemented in Go using [`mautrix`](https://github.com/mautrix/go), with full E2EE (end-to-end encryption) support. Transcription is handled by Python `faster-whisper` through a persistent bridge process.

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
| `MATRIX_PASSWORD` | (required) | Bot account password |
| `PICKLE_KEY` | (required) | Secret key for E2EE store encryption — generate once and keep stable |
| `STORE_PATH` | `/app/store` | Directory for session and E2EE key storage |
| `WHISPER_MODEL` | `large-v3` | faster-whisper model name |
| `WHISPER_LANGUAGE` | `es` | Transcription language code |
| `WHISPER_MODEL_DIR` | `/app/models` | Whisper model cache directory |
| `WHISPER_CPU_THREADS` | `0` | CPU threads for transcription (0 = all cores) |
| `PYTHON_BIN` | `python` | Python interpreter for the transcription bridge |

### 3. Run

```bash
docker compose up -d
```

The bot will download the whisper model on first run (~3 GB, cached in `data/models/`).

## Data persistence

- `data/store/` — Matrix session and E2EE key storage (`crypto.db`). **Do not delete.**
- `data/models/` — Whisper model cache (can be deleted; re-downloads on next start).
