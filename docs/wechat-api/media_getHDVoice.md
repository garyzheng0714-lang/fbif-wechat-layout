# 获取高清语音素材

**接口英文名**：getFromJSSDK

获取 JSSDK uploadVoice 上传的临时语音素材，speex 格式，16K 采样率，比普通接口（amr/8K）更清晰。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/media/get/jssdk?access_token=ACCESS_TOKEN&media_id=MEDIA_ID
```

## 请求参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| media_id | string | 是 | uploadVoice 返回的 serverID |

## 返回

正常返回文件二进制数据。需使用 speex 解码库转码。

## 错误码

| 错误码 | 说明 |
|--------|------|
| 40007 | 无效的媒体ID |
