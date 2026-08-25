# The text to speech engine, Piper on CPU, built on this host on first use.
# Deliberately never pulled as a prebuilt image with the engine inside:
# whoever builds holds the licenses. The install is pinned and the image tag
# hashes this file, so the engine only ever updates when this file changes.
FROM python:3.12-slim
RUN pip install --no-cache-dir piper-tts==1.3.0
# The API is one file. The voices download into DC_MODEL_DIR, the host bind
# that lets them survive the container, and load before the server binds its
# port, so the port answering is the readiness signal. /synthesize takes the
# text plus the language the cockpit detected and answers a wav; anything but
# German falls back to the English voice. One synthesis runs at a time behind
# the lock; /health stays answerable beside it.
# DC_PIPER_VOICE picks the speaker for every language at once, so an answer
# never changes gender when it changes language: male speaks thorsten and
# ryan, female kerstin and amy. The names live here, the variable only selects
# between them, and an unknown value falls back to the default. Only the
# picked pair downloads, the other one costs nothing.
# Half a second of silence follows the speech: piper stops right on the last
# word, and a player that tears its output down at the element's end, iOS and
# Bluetooth do, loses whatever still sits in the output buffer, which was the
# last words. The padding gives the tail room to drain.
RUN printf '%s\n' \
  'import json, os, subprocess, threading, wave' \
  'from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer' \
  'from io import BytesIO' \
  'from piper import PiperVoice' \
  'SPEAKERS = {"male": {"de": "de_DE-thorsten-medium", "en": "en_US-ryan-medium"}, "female": {"de": "de_DE-kerstin-low", "en": "en_US-amy-medium"}}' \
  'VOICES = SPEAKERS.get(os.environ.get("DC_PIPER_VOICE", ""), SPEAKERS["male"])' \
  'model_dir = os.environ["DC_MODEL_DIR"]' \
  'os.chdir(model_dir)' \
  'loaded = {}' \
  'for lang, name in VOICES.items():' \
  '    if not os.path.exists(os.path.join(model_dir, name + ".onnx")):' \
  '        subprocess.run(["python", "-m", "piper.download_voices", name], check=True)' \
  '    loaded[lang] = PiperVoice.load(os.path.join(model_dir, name + ".onnx"))' \
  'lock = threading.Lock()' \
  'class Handler(BaseHTTPRequestHandler):' \
  '    def log_message(self, fmt, *args):' \
  '        pass' \
  '    def answer(self, code, kind, payload):' \
  '        self.send_response(code)' \
  '        self.send_header("Content-Type", kind)' \
  '        self.send_header("Content-Length", str(len(payload)))' \
  '        self.end_headers()' \
  '        self.wfile.write(payload)' \
  '    def do_GET(self):' \
  '        self.answer(200 if self.path == "/health" else 404, "application/json", b"{}")' \
  '    def do_POST(self):' \
  '        if self.path != "/synthesize":' \
  '            self.answer(404, "application/json", b"{}")' \
  '            return' \
  '        try:' \
  '            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))' \
  '            voice = loaded.get(body.get("language"), loaded["en"])' \
  '            out = BytesIO()' \
  '            with lock:' \
  '                with wave.open(out, "wb") as wav_file:' \
  '                    voice.synthesize_wav(body.get("text", ""), wav_file)' \
  '                    pad = wav_file.getframerate() // 2' \
  '                    wav_file.writeframes(b"\x00" * (pad * wav_file.getsampwidth() * wav_file.getnchannels()))' \
  '            self.answer(200, "audio/wav", out.getvalue())' \
  '        except Exception as err:' \
  '            self.answer(400, "application/json", json.dumps({"error": str(err)}).encode())' \
  'ThreadingHTTPServer(("", 8036), Handler).serve_forever()' \
  > /srv/tts.py
CMD ["python", "/srv/tts.py"]
