# 删除永久素材

**接口英文名**：delMaterial

删除不再需要的永久素材。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/material/del_material?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media_id | string | 是 | 要删除的素材 media_id |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok"
}
```

## 错误码

| 错误码 | 描述 |
|--------|------|
| -1 | 系统错误 |
| 40001 | invalid access_token |
| 40007 | invalid media_id |
