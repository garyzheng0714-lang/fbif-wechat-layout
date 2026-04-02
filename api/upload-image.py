"""Upload external image to Alibaba Cloud OSS.

Fetches an image from a given URL (e.g. mmbiz.qpic.cn) and uploads it
to OSS, returning the public CDN URL. Used by the FBIF formatter to
make images paste-compatible with the WeChat editor.
"""

import hashlib
import json
import os
from http.server import BaseHTTPRequestHandler
from urllib.parse import urlparse

import oss2
import requests


def env(key):
    return os.environ[key].strip()


def get_oss_bucket():
    auth = oss2.Auth(env("OSS_ACCESS_KEY_ID"), env("OSS_ACCESS_KEY_SECRET"))
    endpoint = f"https://oss-{env('OSS_REGION')}.aliyuncs.com"
    return oss2.Bucket(auth, endpoint, env("OSS_BUCKET"))


def fetch_image(url):
    """Fetch image bytes from URL, bypassing WeChat hotlink protection."""
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "Referer": "",
    }
    resp = requests.get(url, headers=headers, timeout=15)
    resp.raise_for_status()
    return resp.content, resp.headers.get("Content-Type", "image/jpeg")


def url_to_oss_key(url, content_type):
    """Generate a stable OSS key from the URL hash."""
    url_hash = hashlib.md5(url.encode()).hexdigest()
    ext_map = {
        "image/png": ".png",
        "image/jpeg": ".jpg",
        "image/gif": ".gif",
        "image/webp": ".webp",
    }
    ext = ext_map.get(content_type, ".jpg")
    return f"wechat-images/{url_hash}{ext}"


class handler(BaseHTTPRequestHandler):
    def do_POST(self):
        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length)) if length else {}
            urls = body.get("urls", [])

            if not urls:
                self._respond(400, {"error": "No urls provided"})
                return

            bucket = get_oss_bucket()
            results = {}

            for url in urls[:20]:  # cap at 20 images per request
                try:
                    oss_key = url_to_oss_key(url, "image/jpeg")

                    # Check if already uploaded (avoid re-upload)
                    if bucket.object_exists(oss_key):
                        cdn_url = f"https://{env('OSS_BUCKET')}.oss-{env('OSS_REGION')}.aliyuncs.com/{oss_key}"
                        results[url] = cdn_url
                        continue

                    # Fetch and upload
                    img_data, content_type = fetch_image(url)
                    oss_key = url_to_oss_key(url, content_type)
                    bucket.put_object(oss_key, img_data, headers={"Content-Type": content_type})
                    cdn_url = f"https://{env('OSS_BUCKET')}.oss-{env('OSS_REGION')}.aliyuncs.com/{oss_key}"
                    results[url] = cdn_url

                except Exception as e:
                    results[url] = None  # failed, frontend keeps original

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
