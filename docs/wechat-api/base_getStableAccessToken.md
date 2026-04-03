# 获取稳定版接口调用凭据

**接口英文名**：getStableAccessToken

相比 getAccessToken，此接口更稳定可靠，推荐优先使用。支持普通模式和强制刷新模式。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/stable_token
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| grant_type | string | 是 | 固定值：`client_credential` |
| appid | string | 是 | 账号唯一凭证ID |
| secret | string | 是 | 唯一凭证密钥 |
| force_refresh | boolean | 否 | 默认false。true为强制刷新模式 |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| access_token | string | 获取到的凭证 |
| expires_in | number | 凭证有效时间（秒），最多7200秒 |

## 注意事项

- 与 getAccessToken 获取的调用凭证**完全隔离**
- 普通模式：调用频率 1万次/分钟，50万次/天
- 强制刷新：每天限20次，需间隔30秒
- 建议保留至少512字符的存储空间
- 普通模式下平台提前5分钟更新token

## 错误码

| 错误码 | 描述 |
|--------|------|
| 40013 | 不合法的AppID |
| 40125 | 无效的AppSecret |
| 45009 | 超过天级频率限制 |
| 45011 | API调用过于频繁 |
