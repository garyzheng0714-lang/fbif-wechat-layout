# 获取草稿总数

**接口英文名**：draft_count

获取草稿箱中的草稿总数，不返回详细内容。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/draft/count?access_token=ACCESS_TOKEN
```

## 请求参数

无请求体。

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| total_count | number | 草稿总数 |

## 返回示例

```json
{
  "total_count": 15
}
```
