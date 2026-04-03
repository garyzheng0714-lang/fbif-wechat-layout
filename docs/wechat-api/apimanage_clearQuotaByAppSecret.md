# 使用AppSecret重置API调用次数

**接口英文名**：clearQuotaByAppSecret

通过 AppSecret（而非 access_token）清空每日调用次数，解决 access_token 耗尽无法调用清零接口的问题。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/clear_quota/v2
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| appid | string | 是 | 账号 appid |
| appsecret | string | 是 | AppSecret |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```

## 注意事项

- 每月共 10 次清零机会（含其他清零接口）
- 仅支持 POST 调用
- 不支持第三方平台调用
