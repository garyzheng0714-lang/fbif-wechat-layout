# 创建个性化菜单

**接口英文名**：addConditionalMenu

根据匹配规则为不同用户群展示不同菜单。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/menu/addconditional?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| button | array | 是 | 菜单按钮（1-3个） |
| matchrule | object | 是 | 匹配条件（至少一个非空字段） |

### matchrule 字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| tag_id | string | 用户标签ID |
| client_platform_type | string | 客户端类型：1=iOS，2=Android，3=其他 |

> 隐私保护：不再支持按性别、地区、语言过滤。

## 返回示例

```json
{ "menuid": "208379533" }
```

## 注意事项

- 需先创建默认菜单
- 每天最多创建/删除 2000 次
- 菜单有5分钟缓存
