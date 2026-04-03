# 重置指定API调用次数

**接口英文名**：clearApiQuota

重置指定 API 的每日调用额度。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/openapi/quota/clear?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| cgi_path | string | 是 | API路径，以 `/` 开头，如 `/channels/ec/basics/info/get` |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```

## 注意事项

- 每个账号每月 50 次重置机会
- 目前仅适用于 `/channels/ec/` 前缀的接口
