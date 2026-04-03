# 获取微信推送服务器IP

**接口英文名**：getCallbackIP

获取微信推送服务器 IP 地址列表（向开发者服务器推送消息的来源地址）。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/getcallbackip?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| ip_list | array | 推送服务器IP列表 |

## 返回示例

```json
{
  "ip_list": [
    "106.55.206.146",
    "106.55.206.211"
  ]
}
```

## 注意事项

建议每天请求1次更新列表。跨运营商高峰期可能有丢包。
