# 创建自定义菜单

**接口英文名**：createMenu

创建公众号自定义菜单。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/menu/create?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| button | array | 是 | 一级菜单数组，最多3个 |

### button 字段

| 参数名 | 类型 | 说明 |
|--------|------|------|
| type | string | 类型：click、view、scancode_push、scancode_waitmsg、pic_sysphoto、pic_photo_or_album、pic_weixin、location_select、media_id、article_id、article_view_limited、miniprogram |
| name | string | 菜单标题（一级≤4个汉字，二级≤8个汉字） |
| key | string | 菜单KEY值（click类型，≤128字节） |
| url | string | 网页链接（view/miniprogram类型，≤1024字节） |
| appid | string | 小程序ID |
| pagepath | string | 小程序页面路径 |
| sub_button | array | 二级菜单（最多5个） |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```

## 注意事项

- 最多3个一级菜单，每个一级菜单最多5个二级菜单
- 菜单在用户进入会话或关注页面时刷新
- 测试可取消关注后重新关注
