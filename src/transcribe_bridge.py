import json
import os
import sys

from src.transcriber import Transcriber


def main() -> int:
    model_name = os.environ.get("WHISPER_MODEL", "large-v3")
    language = os.environ.get("WHISPER_LANGUAGE", "es")
    model_dir = os.environ.get("WHISPER_MODEL_DIR", "/app/models")
    cpu_threads = int(os.environ.get("WHISPER_CPU_THREADS", "0"))

    transcriber = Transcriber(
        model_name=model_name,
        language=language,
        model_dir=model_dir,
        cpu_threads=cpu_threads,
    )

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
            audio_path = req["audio_path"]
            text = transcriber.transcribe(audio_path)
            print(json.dumps({"text": text, "error": ""}), flush=True)
        except Exception as exc:
            print(json.dumps({"text": "", "error": str(exc)}), flush=True)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
