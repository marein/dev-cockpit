# The speech to text engine, faster-whisper on CPU, built on this host on
# first use. Deliberately never pulled as a prebuilt image with the engine
# inside: whoever builds holds the licenses. The install is pinned and the
# image tag hashes this file, so the engine only ever updates when this file
# changes.
FROM python:3.12-slim
# requests rides along because faster-whisper 1.2.0 imports it without
# declaring it, and the current huggingface-hub no longer pulls it in.
RUN pip install --no-cache-dir faster-whisper==1.2.0 requests
# The API is one file. The model loads before the server binds its port, so
# the port answering is the readiness signal, and it loads from DC_MODEL_DIR,
# the host bind that lets the download survive the container. /transcribe
# takes the raw clip bytes and answers the text plus the language whisper
# detected per utterance, which is what makes German, English and mixed input
# work without any language setting. One transcription runs at a time behind
# the lock; /health stays answerable beside it.
# DC_WHISPER_MODEL picks the size and carries a generic id, so which model a
# size means is decided here and a stored setting survives swapping it. The
# mapping is also the allowlist: an unknown value would otherwise read as a
# model repository to go and download, so anything off it falls back to the
# default instead.
RUN printf '%s\n' \
  'import json, os, threading' \
  'from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer' \
  'from io import BytesIO' \
  'from faster_whisper import WhisperModel' \
  'MODELS = {"tiny": "tiny", "base": "base", "small": "small", "medium": "medium", "large": "large-v3-turbo"}' \
  'name = MODELS.get(os.environ.get("DC_WHISPER_MODEL", ""), "small")' \
  'model = WhisperModel(name, device="cpu", compute_type="int8", download_root=os.environ["DC_MODEL_DIR"])' \
  'lock = threading.Lock()' \
  'class Handler(BaseHTTPRequestHandler):' \
  '    def log_message(self, fmt, *args):' \
  '        pass' \
  '    def answer(self, code, payload):' \
  '        self.send_response(code)' \
  '        self.send_header("Content-Type", "application/json")' \
  '        self.send_header("Content-Length", str(len(payload)))' \
  '        self.end_headers()' \
  '        self.wfile.write(payload)' \
  '    def do_GET(self):' \
  '        self.answer(200 if self.path == "/health" else 404, b"{}")' \
  '    def do_POST(self):' \
  '        if self.path != "/transcribe":' \
  '            self.answer(404, b"{}")' \
  '            return' \
  '        clip = self.rfile.read(int(self.headers.get("Content-Length", "0")))' \
  '        try:' \
  '            with lock:' \
  '                segments, info = model.transcribe(BytesIO(clip))' \
  '                text = " ".join(part.text.strip() for part in segments).strip()' \
  '            self.answer(200, json.dumps({"text": text, "language": info.language}).encode())' \
  '        except Exception as err:' \
  '            self.answer(400, json.dumps({"error": str(err)}).encode())' \
  'ThreadingHTTPServer(("", 8035), Handler).serve_forever()' \
  > /srv/stt.py
CMD ["python", "/srv/stt.py"]
