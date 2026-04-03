# 上传发表内容中的图片

**接口英文名**：uploadImage

用于上传发表内容（文章或贴图）所需的图片，并获取微信CDN上的图片URL。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/media/uploadimg?access_token=ACCESS_TOKEN
```

## 请求参数

### 查询参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |

### 请求体（multipart/form-data）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media | file | 是 | 图片文件，仅支持 jpg/png 格式，大小需在 1MB 以下 |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| url | string | 微信CDN图片URL |
| errcode | number | 错误码 |
| errmsg | string | 错误描述 |

## 请求示例

```bash
curl -F media=@test.jpg "https://api.weixin.qq.com/cgi-bin/media/uploadimg?access_token=ACCESS_TOKEN"
```

## 返回示例

```json
{
  "url": "http://mmbiz.qpic.cn/XXXXX",
  "errcode": 0,
  "errmsg": "ok"
}
```

## 错误码

| 错误码 | 说明 | 解决方案 |
|--------|------|---------|
| 40005 | invalid file type | 上传素材文件格式不对 |
| 40009 | invalid image size | 图片尺寸太大 |

## 注意事项

1. 上传的图片**不占用素材库限制**
2. 仅支持 jpg/png 格式，大小需在 1MB 以下
3. 返回的 URL 可直接用于 `draft/add` 接口的 content 字段中的 `<img>` 标签
