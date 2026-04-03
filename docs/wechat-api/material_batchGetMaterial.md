# 获取永久素材列表

**接口英文名**：batchGetMaterial

分页获取指定类型的永久素材列表。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/material/batchget_material?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| type | string | 是 | 素材类型：image、video、voice、news |
| offset | number | 是 | 起始位置（从0开始） |
| count | number | 是 | 返回数量（1-20） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| total_count | number | 该类型素材总数 |
| item_count | number | 本次返回数量 |
| item | array | 素材列表 |

每个 item 包含：media_id、update_time、name、url（图片/视频/语音类型），或 content.news_item 数组（图文类型）。

## 错误码

| 错误码 | 描述 |
|--------|------|
| -1 | 系统繁忙 |
| 40001 | invalid access_token |
| 40004 | invalid media type |
