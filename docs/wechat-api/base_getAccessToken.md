# 获取接口调用凭据

**接口英文名**：getAccessToken

本接口用于获取全局唯一后台接口调用凭据（Access Token）。凭证有效期为 7200 秒，开发者需妥善保存。

> 接口必须在服务器端调用，不可在前端（小程序、网页、APP等）直接调用。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/token
```

## 请求参数

### 查询参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| grant_type | string | 是 | 填写 `client_credential` |
| appid | string | 是 | 账号唯一凭证（AppID） |
| secret | string | 是 | 凭证密钥（AppSecret） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| access_token | string | 获取到的凭证 |
| expires_in | number | 凭证有效时间（秒），目前为 7200 秒内的值 |

## 请求示例

```
GET https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=APPID&secret=APPSECRET
```

## 返回示例

```json
{
  "access_token": "ACCESS_TOKEN",
  "expires_in": 7200
}
```

## 错误码

| 错误码 | 错误描述 | 解决方案 |
|--------|--------|--------|
| -1 | system error | 系统繁忙，请稍候重试 |
| 40001 | invalid credential | AppSecret 错误或 access_token 无效 |
| 40002 | invalid grant_type | 不合法的凭证类型 |
| 40013 | invalid appid | AppID 不合法 |
| 40125 | 不合法的 secret | 检查 secret 正确性 |
| 40164 | IP地址不在白名单 | 在接口 IP 白名单中设置 |
| 40243 | AppSecret已被冻结 | 解冻后再调用 |
| 41004 | appsecret missing | 缺少 secret 参数 |
| 50004 | 禁止使用 token 接口 | 账号权限限制 |
| 50007 | 账号已冻结 | 账号被冻结 |

## 注意事项

1. 不同应用类型的 Access Token 互相隔离，仅支持调用应用类型接口
2. AppSecret 是敏感凭证，需妥善保管以防泄露风险
3. 开发者可对 AppSecret 进行冻结以提高账号安全性
4. AppSecret 冻结后无法获取 Access token（错误码 40243），但不影响账号基本功能和第三方授权调用
