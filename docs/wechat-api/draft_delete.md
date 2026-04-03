# 删除草稿

**接口英文名**：draft_delete

删除草稿箱中指定的草稿。**此操作无法撤销，请谨慎操作。**

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/draft/delete?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media_id | string | 是 | 要删除的草稿 media_id |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok"
}
```

## 错误码

| 错误码 | 描述 | 解决方案 |
|--------|------|---------|
| 40007 | invalid media_id | 检查 media_id 是否正确 |
