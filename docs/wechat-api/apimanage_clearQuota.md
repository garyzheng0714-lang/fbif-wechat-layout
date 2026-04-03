# 重置API调用次数

**接口英文名**：clearQuota

通过 access_token 清空服务端接口的每日调用次数。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/clear_quota?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| appid | string | 是 | 要清空的账号 appid |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```

## 注意事项

- 每个账号每月共 10 次清零机会
- 错误码 48006 表示达到月度上限
