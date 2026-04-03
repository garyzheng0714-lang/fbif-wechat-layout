# 查询API调用额度

**接口英文名**：getApiQuota

查询服务端接口的每日调用额度、已用次数和速率限制。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/openapi/quota/get?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| cgi_path | string | 是 | API请求路径，如 `/cgi-bin/message/custom/send`，以 `/` 开头 |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| quota.daily_limit | number | 每日允许最大调用次数 |
| quota.used | number | 今日已用次数 |
| quota.remain | number | 今日剩余次数 |
| rate_limit.call_count | number | 每个周期允许调用次数 |
| rate_limit.refresh_second | number | 周期时长（秒） |

## 错误码

| 错误码 | 描述 |
|--------|------|
| 76021 | 路径不存在 |
| 76022 | 无权限查询 |
