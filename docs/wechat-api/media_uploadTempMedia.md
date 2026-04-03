# 上传临时素材

**接口英文名**：uploadTempMedia

上传临时多媒体文件到微信服务器。**临时素材 3 天后过期。**

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/media/upload?access_token=ACCESS_TOKEN&type=TYPE
```

## 请求参数

### 查询参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| type | string | 是 | 类型：image、voice、video、thumb |

### 请求体（multipart/form-data）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media | file | 是 | 媒体文件 |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| type | string | 媒体文件类型 |
| media_id | string | 媒体文件标识 |
| created_at | number | 上传时间戳 |

## 文件大小限制

| 类型 | 格式 | 大小限制 |
|------|------|----------|
| 图片 | PNG/JPEG/JPG/GIF | 10MB |
| 语音 | AMR/MP3 | 2MB，≤60秒 |
| 视频 | MP4 | 10MB |
| 缩略图 | JPG | 64KB |

## 请求示例

```bash
curl -F media=@test.jpg "https://api.weixin.qq.com/cgi-bin/media/upload?access_token=ACCESS_TOKEN&type=image"
```

## 注意事项

- 媒体文件在微信后台保存 3 天，之后 media_id 失效
- 临时素材不能通过 del_material 接口删除
