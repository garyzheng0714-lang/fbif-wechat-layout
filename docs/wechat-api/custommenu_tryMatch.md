# 测试个性化菜单匹配

**接口英文名**：tryMatchMenu

测试某用户看到的菜单配置。每日测试限制 20,000 次。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/menu/trymatch?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| user_id | string | 是 | 用户 OpenID 或微信号 |

## 返回参数

返回匹配到的菜单 button 数组，结构同 getMenu。
