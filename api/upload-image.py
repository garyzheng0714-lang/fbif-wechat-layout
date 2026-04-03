"""Upload images to Alibaba Cloud OSS for WeChat paste compatibility.

Supports two modes:
1. URL mode: fetch from external URL (e.g. mmbiz.qpic.cn) and upload
2. Base64 mode: upload base64-encoded images directly (for DOCX embedded images)
"""

import base64 as b64mod
import hashlib
import json
import os
from http.server import BaseHTTPRequestHandler

import oss2
import requests


def env(key):
    return os.environ[key].strip()


def get_oss_bucket():
    auth = oss2.Auth(env("OSS_ACCESS_KEY_ID"), env("OSS_ACCESS_KEY_SECRET"))
    endpoint = f"https://oss-{env('OSS_REGION')}.aliyuncs.com"
    return oss2.Bucket(auth, endpoint, env("OSS_BUCKET"))


def make_cdn_url(oss_key):
    return f"https://{env('OSS_BUCKET')}.oss-{env('OSS_REGION')}.aliyuncs.com/{oss_key}"


def fetch_image(url):
    """Fetch image bytes from URL, bypassing WeChat hotlink protection."""
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "Referer": "",
    }
    resp = requests.get(url, headers=headers, timeout=15)
    resp.raise_for_status()
    return resp.content, resp.headers.get("Content-Type", "image/jpeg")


EXT_MAP = {
    "image/png": ".png",
    "image/jpeg": ".jpg",
    "image/gif": ".gif",
    "image/webp": ".webp",
}


def url_to_oss_key(url, content_type="image/jpeg"):
    """Stable OSS key from URL hash with correct extension."""
    url_hash = hashlib.md5(url.encode()).hexdigest()
    ext = EXT_MAP.get(content_type, ".jpg")
    return f"wechat-images/{url_hash}{ext}"


def data_to_oss_key(img_bytes, content_type):
    """OSS key from image data hash with correct extension."""
    data_hash = hashlib.md5(img_bytes).hexdigest()
    ext = EXT_MAP.get(content_type, ".jpg")
    return f"wechat-images/{data_hash}{ext}"


class handler(BaseHTTPRequestHandler):
    def do_POST(self):
        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length)) if length else {}
            urls = body.get("urls", [])
            base64_images = body.get("base64_images", {})

            if not urls and not base64_images:
                self._respond(400, {"error": "No urls or base64_images provided"})
                return

            bucket = get_oss_bucket()
            results = {}

            # URL-based upload (for markdown external images)
            for url in urls[:20]:
                try:
                    img_data, content_type = fetch_image(url)
                    if len(img_data) > 10 * 1024 * 1024:
                        results[url] = None
                        continue
                    oss_key = url_to_oss_key(url, content_type)
                    if bucket.object_exists(oss_key):
                        results[url] = make_cdn_url(oss_key)
                        continue
                    bucket.put_object(oss_key, img_data, headers={"Content-Type": content_type})
                    results[url] = make_cdn_url(oss_key)

                except Exception:
                    results[url] = None

            # Base64 direct upload (for DOCX embedded images)
            for name, data_uri in list(base64_images.items())[:20]:
                try:
                    header, b64data = data_uri.split(",", 1)
                    content_type = header.split(":")[1].split(";")[0]
                    if content_type not in EXT_MAP:
                        results[name] = None
                        continue
                    img_data = b64mod.b64decode(b64data)
                    if len(img_data) > 10 * 1024 * 1024:
                        results[name] = None
                        continue

                    oss_key = data_to_oss_key(img_data, content_type)

                    if bucket.object_exists(oss_key):
                        results[name] = make_cdn_url(oss_key)
                        continue

                    bucket.put_object(oss_key, img_data, headers={"Content-Type": content_type})
                    results[name] = make_cdn_url(oss_key)

                except Exception:
                    results[name] = None

            self._respond(200, {"results": results})

        except Exception as e:
            self._respond(500, {"error": str(e)})

    def _respond(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def do_OPTIONS(self):
        self._respond(200, {})
