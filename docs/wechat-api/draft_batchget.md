# 获取草稿列表

**接口英文名**：draft_batchget

获取草稿箱中的草稿列表。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/draft/batchget?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| offset | number | 是 | 起始位置（0为第一条） |
| count | number | 是 | 返回数量（1-20） |
| no_content | number | 否 | 1=不返回content字段；0=返回（默认） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| total_count | number | 草稿总数 |
| item_count | number | 本次返回数量 |
| item | array | 草稿列表 |

每个 item 包含：
- `media_id` - 草稿ID
- `content.news_item` - 文章数组（title, author, digest, content 等）
- `update_time` - 更新时间
