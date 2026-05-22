# matrix-transcript-bot

A self-hosted Matrix bot that transcribes audio messages into text.

The Matrix integration is implemented in Go using [`mautrix`](https://github.com/mautrix/go), and transcription is handled by Python `faster-whisper` through a persistent bridge process.

## How it works

1. A user sends `m.audio` or `m.video` in a room where the bot is present.
2. The bot reacts with 🤖 while processing.
3. The Go bot downloads the media from Matrix.
4. The bot asks the Python bridge to transcribe with `faster-whisper`.
5. On success, the bot removes 🤖 and replies with text.
6. On failure, the bot removes 🤖 and reacts with ❌.

## Setup

### 1. Create a bot account

Create a Matrix account for the bot on your homeserver.

### 2. Configure

```bash
cp .env.example .env
```

Environment variables:

- `MATRIX_HOMESERVER` (required)
- `MATRIX_USER_ID` (required)
- `MATRIX_PASSWORD` (required)
- `STORE_PATH` (default `/app/store`)
- `WHISPER_MODEL` (default `large-v3`)
- `WHISPER_LANGUAGE` (default `es`)
- `WHISPER_MODEL_DIR` (default `/app/models`)
- `WHISPER_CPU_THREADS` (default `0`)
- `PYTHON_BIN` (default `python`)

### 3. Run

```bash
docker compose up -d
```

## Data persistence

- `data/store/` — Matrix session storage
- `data/models/` — Whisper model cache
