# 上传图文消息素材（已废弃，建议用草稿箱）

**接口英文名**：uploadnewsmsg

> 此接口已更新为草稿箱功能，推荐使用 `draft/add` 接口替代。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/media/uploadnews?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| articles | array | 是 | 图文消息，1-8条 |

### articles 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| title | string | 是 | 标题 |
| author | string | 否 | 作者 |
| thumb_media_id | string | 是 | 缩略图 media_id |
| content | string | 是 | 正文HTML |
| content_source_url | string | 否 | 阅读原文链接 |
| digest | string | 否 | 摘要 |
| show_cover_pic | number | 否 | 是否显示封面（1显示/0不显示） |
| need_open_comment | number | 否 | 是否打开评论 |
| only_fans_can_comment | number | 否 | 是否仅粉丝可评论 |

## 返回参数

| 字段 | 类型 | 说明 |
|------|------|------|
| type | string | `news` |
| media_id | string | 图文消息 media_id |
| created_at | string | 上传时间戳 |
