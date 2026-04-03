# 查询RID信息

**接口英文名**：getRidInfo

通过 rid（请求ID）查询API调用错误的详细信息，帮助定位问题。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/openapi/rid/get?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| rid | string | 是 | API错误返回的 rid |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| request.invoke_time | number | 请求时间戳 |
| request.cost_in_ms | number | 执行耗时（毫秒） |
| request.request_url | string | URL参数 |
| request.request_body | string | POST参数 |
| request.response_body | string | API响应 |
| request.client_ip | string | 客户端IP |

## 注意事项

- 仅能查询自己账号的 rid
- rid 7天后过期
- 不支持 SNS 类接口
