# 网络通信检测

**接口英文名**：callbackCheck

帮助开发者排查回调连接失败问题，执行 DNS 解析和 Ping 检测。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/callback/check?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| action | string | 是 | 检测类型：dns、ping、all |
| check_operator | string | 是 | 运营商：CHINANET（电信）、UNICOM（联通）、CAP（腾讯）、DEFAULT（自动） |

## 返回参数

```json
{
  "dns": [
    { "ip": "string", "real_operator": "string" }
  ],
  "ping": [
    { "ip": "string", "from_operator": "string", "package_loss": "string", "time": "string" }
  ]
}
```

## 错误码

| 错误码 | 描述 |
|--------|------|
| 40201 | invalid url（回调未配置） |
| 40202 | invalid action |
| 40203 | invalid operator |
