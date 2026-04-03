# 获取微信API服务器IP

**接口英文名**：getApiDomainIP

获取微信 API 服务器的出口 IP 地址列表，用于安全配置。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/get_api_domain_ip?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| ip_list | array | 微信服务器IP地址列表 |

## 返回示例

```json
{
  "ip_list": [
    "101.89.47.18",
    "101.91.34.103"
  ]
}
```

## 注意事项

建议每天请求1次以更新 IP 列表。
