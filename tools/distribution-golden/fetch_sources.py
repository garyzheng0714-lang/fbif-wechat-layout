#!/usr/bin/env python3
"""在 A 机上抓样稿的两样原始数据：FoodTalks 已发布正文、编辑手发在头条号上的正文。

用法（A 机）：python3 fetch_sources.py 286 287 ...（任务编号）
结果在 /tmp/golden/：ft-<编号>.json、tt-<编号>.json。头条号按标题搜，作者是自家号、标题前段对得上才算。
FoodTalks 正式环境按 IP 白名单认，不能带浏览器请求头，带了会回 429。
"""
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

OUT = "/tmp/golden"
PC_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36"
MOBILE_UA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"


def load_env(path="/var/www/api/publish.env"):
    env = {}
    for line in open(path):
        line = line.strip()
        if "=" in line and not line.startswith("#"):
            key, value = line.split("=", 1)
            env[key] = value.strip().strip('"').strip("'")
    return env


def get(url, headers):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as resp:
        return resp.read()


def squash(text):
    return re.sub(r"[\s\W_]+", "", text or "")


def fetch_foodtalks(env, job_id, news_id):
    url = env["FOODTALKS_BASE_URL"].rstrip("/") + "/news/admin/news/%d" % news_id
    headers = {"Authorization": "Bearer " + env["FOODTALKS_TOKEN"], "Accept": "application/json", "User-Agent": ""}
    try:
        data = json.loads(get(url, headers))
    except urllib.error.HTTPError as err:
        return "HTTP %d" % err.code
    if data.get("code") != 0:
        return "code %s" % data.get("code")
    json.dump(data["data"], open("%s/ft-%d.json" % (OUT, job_id), "w"), ensure_ascii=False)
    return "ok"


def fetch_toutiao(job_id, title):
    query = urllib.parse.urlencode({"dvpf": "pc", "source": "input", "keyword": title})
    page = get("https://so.toutiao.com/search?" + query, {"User-Agent": PC_UA, "Accept": "text/html"}).decode("utf-8", "ignore")
    ids = []
    for match in re.finditer(r"toutiao\.com/(?:a|article/|group/|i)(\d{16,20})", page):
        if match.group(1) not in ids:
            ids.append(match.group(1))
    for group_id in ids[:12]:
        info = json.loads(get("https://m.toutiao.com/i%s/info/v2/" % group_id, {"User-Agent": MOBILE_UA})).get("data") or {}
        author = info.get("source") or (info.get("media_user") or {}).get("screen_name") or ""
        if "FBIF" in author and squash(info.get("title"))[:12] == squash(title)[:12]:
            record = {"group_id": group_id, "title": info.get("title"), "content": info.get("content")}
            json.dump(record, open("%s/tt-%d.json" % (OUT, job_id), "w"), ensure_ascii=False)
            return "found " + group_id
        time.sleep(0.5)
    return "not found"


def main():
    os.makedirs(OUT, exist_ok=True)
    env = load_env()
    wanted = {int(arg) for arg in sys.argv[1:]}
    jobs = json.load(open("/var/www/api/data/publish-jobs.json"))["jobs"]
    for job in jobs:
        if job["id"] not in wanted:
            continue
        foodtalks = fetch_foodtalks(env, job["id"], job["foodtalks_news_id"])
        toutiao = fetch_toutiao(job["id"], job["title"])
        print(job["id"], job["title"][:20], "| FoodTalks:", foodtalks, "| 头条号:", toutiao, flush=True)
        time.sleep(2)


if __name__ == "__main__":
    main()
