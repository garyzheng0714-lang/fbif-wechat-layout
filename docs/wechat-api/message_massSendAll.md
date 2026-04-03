# 按标签群发消息

**接口英文名**：massSendAll

根据用户标签群发消息。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/message/mass/sendall?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| filter | object | 是 | 接收者设置 |
| filter.is_to_all | boolean | 是 | 是否发给全部用户 |
| filter.tag_id | number | 否 | 目标标签ID |
| msgtype | string | 是 | 消息类型：mpnews、text、voice、image、mpvideo、wxcard |
| clientmsgid | string | 否 | 防重入ID（24小时内有效） |

## 返回参数

```json
{
  "errcode": 0,
  "errmsg": "send job submission success",
  "msg_id": 34182,
  "msg_data_id": 206227730
}
```

## 注意事项

- 图文消息群发前会进行原创校验
- 成功响应仅表示任务提交成功，非完成
- 完成后通过事件推送通知回调URL
- 仅认证公众号/服务号可用
