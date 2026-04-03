# 获取JS-SDK临时票据

**接口英文名**：getTicket

获取 JS-SDK 的 api_ticket，有效期 7200 秒。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/ticket/getticket?access_token=ACCESS_TOKEN&type=jsapi
```

## 请求参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| type | string | 是 | `jsapi`（JS-SDK）或 `wx_card`（卡券） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| ticket | string | 临时票据 |
| expires_in | number | 有效期（秒） |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok",
  "ticket": "bxLdikRXVbTPdHSM05e5u5sUoXNKdvsdshFKA",
  "expires_in": 7200
}
```

## 注意事项

频繁刷新 ticket 会导致 API 调用受限。应在服务端缓存并定时更新。
