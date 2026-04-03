# 获取临时素材

**接口英文名**：getMedia

获取临时素材文件。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/media/get?access_token=ACCESS_TOKEN&media_id=MEDIA_ID
```

## 请求参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| media_id | string | 是 | 媒体文件ID |

## 返回

- 视频类型返回 JSON：`{ "video_url": "DOWN_URL" }`
- 其他类型直接返回素材二进制内容

## 错误码

| 错误码 | 描述 |
|--------|------|
| -1 | 系统错误 |
| 40001 | invalid access_token |
| 40007 | invalid media_id |
