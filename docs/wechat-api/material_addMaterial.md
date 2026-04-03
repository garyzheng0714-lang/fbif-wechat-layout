# 上传永久素材

**接口英文名**：addMaterial

新增永久素材（图片、语音、视频、缩略图）到素材库。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/material/add_material?access_token=ACCESS_TOKEN&type=TYPE
```

## 请求参数

### 查询参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| type | string | 是 | 素材类型：image、voice、video、thumb |

### 请求体（multipart/form-data）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media | file | 是 | 媒体文件 |
| description | object | 否 | 视频素材的描述信息（title、introduction） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| media_id | string | 新增素材的 media_id |
| url | string | 图片素材的 URL（仅图片类型返回） |

## 文件大小限制

| 类型 | 格式 | 大小限制 |
|------|------|----------|
| 图片 | bmp/png/jpeg/jpg/gif | 10MB |
| 语音 | mp3/wma/wav/amr | 2MB，≤60秒 |
| 视频 | MP4 | 10MB |
| 缩略图 | JPG | 64KB |

## 存储上限

- 图片和图文素材：100,000
- 其他素材：1,000

## 注意事项

- 永久图片素材的 URL 仅在腾讯系域名内可用，外部域名使用会被屏蔽
- 用于封面图（thumb_media_id）的素材必须通过此接口上传
