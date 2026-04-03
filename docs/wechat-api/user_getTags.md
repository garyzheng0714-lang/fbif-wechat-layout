# 获取用户标签列表

**接口英文名**：getTags

获取公众号创建的所有标签。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/tags/get?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| tags | array | 标签列表 |
| tags[].id | number | 标签ID |
| tags[].name | string | 标签名称 |
| tags[].count | number | 该标签下粉丝数 |

## 返回示例

```json
{
  "tags": [
    { "id": 1, "name": "每日可乐爱好者", "count": 0 },
    { "id": 2, "name": "明星组", "count": 0 }
  ]
}
```
