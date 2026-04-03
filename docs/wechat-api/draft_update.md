# 更新草稿

**接口英文名**：draft_update

修改草稿箱中的图文或图片消息草稿。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/draft/update?access_token=ACCESS_TOKEN
```

## 请求参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media_id | string | 是 | 要修改的草稿 media_id |
| index | number | 是 | 多图文中的文章位置（从0开始） |
| articles | object | 是 | 文章信息，字段同 draft_add |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok"
}
```

## 注意事项

- content 中会去除 JS
- 图片 URL 必须来源于 `uploadimg` 接口，外部图片 URL 将被过滤
- 最多2万字符
