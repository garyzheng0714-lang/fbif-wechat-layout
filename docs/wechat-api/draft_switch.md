# 草稿箱开关设置

**接口英文名**：draft_switch

设置或查询草稿箱和发布功能的开关状态。

> 开关开启后不可逆，无法从开启的状态回到关闭。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/draft/switch?access_token=ACCESS_TOKEN&checkonly=1
```

## 请求参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| checkonly | number | 否 | 仅检查状态时传1 |

## 返回参数

| 参数 | 类型 | 说明 |
|------|------|------|
| errcode | number | 错误码 |
| errmsg | string | 错误信息 |
| is_open | number | 0（关闭）或1（开启） |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok",
  "is_open": 0
}
```
