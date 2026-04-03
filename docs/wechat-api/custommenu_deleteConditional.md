# 删除个性化菜单

**接口英文名**：deleteConditionalMenu

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/menu/delconditional?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| menuid | string | 是 | 要删除的菜单ID |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```

## 注意事项

- 每天最多 2000 次删除
- 需要已认证公众号
