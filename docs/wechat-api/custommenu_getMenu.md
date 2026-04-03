# 获取自定义菜单配置

**接口英文名**：getMenu

获取通过 API 创建的自定义菜单配置（含个性化菜单）。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/menu/get?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| menu | object | 默认菜单信息 |
| conditionalmenu | array | 个性化菜单列表 |

### menu.button

| 参数名 | 类型 | 说明 |
|--------|------|------|
| type | string | 响应类型 |
| name | string | 标题 |
| key | string | KEY值 |
| url | string | 链接 |
| sub_button | array | 二级菜单 |

## 返回示例

```json
{
  "menu": {
    "button": [
      { "type": "click", "name": "今日歌曲", "key": "V1001_TODAY_MUSIC" }
    ]
  }
}
```
