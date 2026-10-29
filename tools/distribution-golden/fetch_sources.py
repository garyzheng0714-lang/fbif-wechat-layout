#!/usr/bin/env python3
"""在 A 机上抓样稿的 FoodTalks 已发布正文（编辑手发稿由 B 机的 fetch_platforms.mjs 抓）。

用法（A 机）：sudo python3 fetch_sources.py 286 287 ...（任务编号）
结果在 /tmp/golden/：ft-<编号>.json。
FoodTalks 正式环境按 IP 白名单认，不能带浏览器请求头，带了会回 429。
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

OUT = "/tmp/golden"


# 和 fbif-api 一样先读 publish.env，再叠上 FBIF 中台下发的密钥文件（FOODTALKS_TOKEN 在后者），同名以后读的为准。
def load_env(paths=("/var/www/api/publish.env", "/etc/fbif-hub/env/layout.env")):
    env = {}
    for path in paths:
        for line in open(path):
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                key, value = line.split("=", 1)
                env[key] = value.strip().strip('"').strip("'")
    return env


def get(url, headers):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as resp:
        return resp.read()


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


def main():
    os.makedirs(OUT, exist_ok=True)
    env = load_env()
    wanted = {int(arg) for arg in sys.argv[1:]}
    jobs = json.load(open("/var/www/api/data/publish-jobs.json"))["jobs"]
    for job in jobs:
        if job["id"] not in wanted:
            continue
        print(job["id"], job["title"][:20], "| FoodTalks:", fetch_foodtalks(env, job["id"], job["foodtalks_news_id"]), flush=True)
        time.sleep(1)


if __name__ == "__main__":
    main()
