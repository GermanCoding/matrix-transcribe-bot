FROM golang:1.24 AS go-builder

WORKDIR /src
COPY go.mod .
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/matrix-transcribe-bot ./cmd/bot

FROM python:3.12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY src/__init__.py src/transcriber.py src/transcribe_bridge.py src/
COPY --from=go-builder /out/matrix-transcribe-bot /app/matrix-transcribe-bot

RUN mkdir -p /app/store /app/models

CMD ["/app/matrix-transcribe-bot"]
