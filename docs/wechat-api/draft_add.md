# 新增草稿

**接口英文名**：draft_add

本接口用于新增常用的素材到草稿箱。上传至草稿箱的素材被群发或发布后会自动移除；新增的草稿也可在公众平台官网草稿箱中查看和管理。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/draft/add?access_token=ACCESS_TOKEN
```

## 请求参数

### 查询参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |

### 请求体

```json
{
  "articles": [
    {
      "article_type": "news",
      "title": "标题",
      "author": "作者",
      "digest": "摘要",
      "content": "<p>正文HTML</p>",
      "content_source_url": "https://example.com",
      "thumb_media_id": "MEDIA_ID",
      "need_open_comment": 0,
      "only_fans_can_comment": 0
    }
  ]
}
```

## articles 数组字段详解

### 基础字段

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| article_type | string | 否 | 文章类型：`news`（图文消息，默认）或 `newspic`（图片消息） |
| title | string | 是 | 标题，不超过32个字 |
| author | string | 否 | 作者，不超过16个字 |
| digest | string | 否 | 摘要，不超过128个字。未填写则默认抓取正文前54个字 |
| content | string | 是 | 正文HTML内容，不超过2万字符，小于1MB。**图片URL必须来自 `uploadimg` 接口；外部图片URL将被过滤** |
| content_source_url | string | 否 | 原文地址，不超过1KB |
| thumb_media_id | string | 条件必填 | 封面图片素材id（必须是永久MediaID，article_type为news时必填） |
| need_open_comment | number | 否 | 是否打开评论：0（不打开，默认）或1（打开） |
| only_fans_can_comment | number | 否 | 是否粉丝才可评论：0（所有人可评论，默认）或1 |

### 封面裁剪字段

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| pic_crop_235_1 | string | 否 | 封面裁剪为2.35:1规格的坐标，格式 X1_Y1_X2_Y2 |
| pic_crop_1_1 | string | 否 | 封面裁剪为1:1规格的坐标 |

### 图片消息专用字段

#### image_info

```json
{
  "image_list": [
    { "image_media_id": "MEDIA_ID" }
  ]
}
```

图片消息中的图片，最多20张，首张为封面图。

#### cover_info

```json
{
  "crop_percent_list": [
    {
      "ratio": "1_1",
      "x1": "0.166454",
      "y1": "0",
      "x2": "0.833545",
      "y2": "1"
    }
  ]
}
```

支持 ratio：`1_1`、`16_9`、`2.35_1`

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| media_id | string | 草稿的 media_id |

## 返回示例

```json
{
  "media_id": "MEDIA_ID"
}
```

## 错误码

| 错误码 | 错误描述 | 解决方案 |
|--------|---------|---------|
| 53404 | 账号已被限制带货能力 | 请删除商品后重试 |
| 53405 | 插入商品信息有误 | 检查参数及商品状态 |
| 53406 | 请先开通带货能力 | 开通商城能力 |

## 关键注意事项

1. **content 中的图片 URL 必须通过 `/cgi-bin/media/uploadimg` 接口获取**，外部图片 URL 会被过滤
2. 仅限服务器端调用
3. 支持公众号和服务号
4. 支持第三方平台代商家调用（权限集ID：11、100）
