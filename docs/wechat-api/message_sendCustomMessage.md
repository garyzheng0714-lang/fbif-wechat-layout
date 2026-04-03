# 发送客服消息

**接口英文名**：sendCustomMessage

在特定交互场景后向用户发送客服消息。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/message/custom/send?access_token=ACCESS_TOKEN
```

## 发送额度

| 场景 | 额度 | 有效期 |
|------|------|--------|
| 用户发送消息 | 5条 | 48小时 |
| 点击菜单 | 3条 | 1分钟 |
| 关注公众号 | 3条 | 1分钟 |
| 扫描二维码 | 3条 | 1分钟 |

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| touser | string | 是 | 用户 OpenID |
| msgtype | string | 是 | 消息类型 |
| customservice | object | 否 | 指定客服账号 |

## 支持的消息类型

| 类型 | 说明 | 必填字段 |
|------|------|----------|
| text | 文本 | content |
| image | 图片 | media_id |
| voice | 语音 | media_id |
| video | 视频 | media_id, thumb_media_id |
| music | 音乐 | title, musicurl, thumb_media_id |
| news | 外链图文 | articles数组 |
| mpnewsarticle | 公众号图文 | article_id |
| msgmenu | 菜单消息 | list数组 |
| wxcard | 卡券 | card_id |
| miniprogrampage | 小程序卡片 | title, appid, pagepath |

## 返回示例

```json
{ "errcode": 0, "errmsg": "ok" }
```
