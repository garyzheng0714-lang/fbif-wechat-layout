"""Push formatted article to WeChat Official Account drafts.

Flow:
1. Get access_token
2. Find all <img> in content HTML
3. Download each image → upload to WeChat CDN via /cgi-bin/media/uploadimg
4. Replace image URLs with mmbiz.qpic.cn URLs in content
5. Call /cgi-bin/draft/add to create draft
"""

import base64
import json
import os
import re
from http.server import BaseHTTPRequestHandler

import requests

# 1x1 white PNG as fallback cover
DEFAULT_COVER_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAGQAAABkCAYAAABw4pVUAAAALElEQVR42u3BAQ"
    "EAAACCIP+vbkhAAQAAAAAAAAAAAAAAAAAAAAAAAAB8GXJkAAH72eMKAAAAAElF"
    "TkSuQmCC"
)


def env(key):
    return os.environ[key].strip()


def get_access_token():
    resp = requests.get("https://api.weixin.qq.com/cgi-bin/token", params={
        "grant_type": "client_credential",
        "appid": env("WECHAT_APPID"),
        "secret": env("WECHAT_APPSECRET"),
    }, timeout=10)
    data = resp.json()
    if "access_token" not in data:
        raise Exception(f"获取 access_token 失败: {data.get('errmsg', data)}")
    return data["access_token"]


def download_image(img_url):
    """Download image bytes from URL."""
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "Referer": "",
    }
    resp = requests.get(img_url, headers=headers, timeout=15)
    resp.raise_for_status()
    content_type = resp.headers.get("Content-Type", "image/jpeg")
    return resp.content, content_type


def upload_image_to_wechat_cdn(token, img_data, content_type):
    """Upload image to WeChat CDN (for article content images)."""
    ext_map = {"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}
    ext = ext_map.get(content_type, ".jpg")
    url = f"https://api.weixin.qq.com/cgi-bin/media/uploadimg?access_token={token}"
    files = {"media": (f"image{ext}", img_data, content_type)}
    resp = requests.post(url, files=files, timeout=30)
    data = resp.json()
    if "url" not in data:
        raise Exception(f"图片上传失败: {data.get('errmsg', data)}")
    return data["url"]


def upload_thumb_material(token, img_data, content_type):
    """Upload image as permanent material (for cover/thumb_media_id)."""
    ext_map = {"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}
    ext = ext_map.get(content_type, ".jpg")
    url = f"https://api.weixin.qq.com/cgi-bin/material/add_material?access_token={token}&type=image"
    files = {"media": (f"cover{ext}", img_data, content_type)}
    resp = requests.post(url, files=files, timeout=30)
    data = resp.json()
    if "media_id" not in data:
        raise Exception(f"封面上传失败: {data.get('errmsg', data)}")
    return data["media_id"]


def replace_images_in_html(token, html):
    """Find all <img src="http..."> and replace with WeChat CDN URLs."""
    img_pattern = re.compile(r'<img\s+([^>]*?)src="(https?://[^"]+)"', re.IGNORECASE)
    urls_found = set(m.group(2) for m in img_pattern.finditer(html))

    url_map = {}
    first_img_data = None  # Save first image for cover
    first_img_ct = None
    errors = []
    for url in urls_found:
        try:
            img_data, content_type = download_image(url)
            if first_img_data is None:
                first_img_data = img_data
                first_img_ct = content_type
            wechat_url = upload_image_to_wechat_cdn(token, img_data, content_type)
            url_map[url] = wechat_url
        except Exception as e:
            errors.append({"url": url, "error": str(e)})

    # Replace URLs in HTML
    for old_url, new_url in url_map.items():
        html = html.replace(old_url, new_url)

    # Remove referrerpolicy attributes (not needed for WeChat CDN URLs)
    html = re.sub(r'\s*referrerpolicy="no-referrer"', '', html)

    return html, len(url_map), errors, first_img_data, first_img_ct


def create_draft(token, title, content, thumb_media_id=None):
    """Create a draft in WeChat Official Account."""
    url = f"https://api.weixin.qq.com/cgi-bin/draft/add?access_token={token}"
    article = {"title": title, "content": content}
    if thumb_media_id:
        article["thumb_media_id"] = thumb_media_id
    payload = {"articles": [article]}
    resp = requests.post(url, json=payload, timeout=30)
    data = resp.json()
    if "media_id" not in data:
        raise Exception(f"创建草稿失败: {data.get('errmsg', data)}")
    return data["media_id"]


class handler(BaseHTTPRequestHandler):
    def do_POST(self):
        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length)) if length else {}

            title = body.get("title", "").strip()
            content = body.get("content", "").strip()

            if not title or not content:
                self._respond(400, {"error": "缺少 title 或 content"})
                return

            # Step 1: Get access token
            token = get_access_token()

            # Step 2: Upload images to WeChat CDN and replace URLs
            content, img_count, img_errors, first_img, first_ct = replace_images_in_html(token, content)

            # Step 3: Upload cover image as permanent material
            cover_data = first_img or DEFAULT_COVER_PNG
            cover_ct = first_ct or "image/png"
            thumb_id = upload_thumb_material(token, cover_data, cover_ct)

            # Step 4: Create draft
            media_id = create_draft(token, title, content, thumb_id)

            self._respond(200, {
                "media_id": media_id,
                "images_uploaded": img_count,
                "image_errors": img_errors,
            })

        except Exception as e:
            self._respond(500, {"error": str(e)})

    def _respond(self, status, data):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.end_headers()
        self.wfile.write(json.dumps(data, ensure_ascii=False).encode())

    def do_OPTIONS(self):
        self._respond(200, {})
