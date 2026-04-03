# 获取用户基本信息

**接口英文名**：getUserInfo

根据 OpenID 获取关注者基本信息。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/user/info?access_token=ACCESS_TOKEN&openid=OPENID&lang=zh_CN
```

## 请求参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| access_token | string | 是 | 接口调用凭证 |
| openid | string | 是 | 用户OpenID |
| lang | string | 否 | 语言版本（zh_CN） |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| subscribe | number | 是否关注（0=未关注，1=已关注） |
| openid | string | 用户标识 |
| subscribe_time | number | 关注时间戳 |
| unionid | string | UnionID（需绑定开放平台） |
| remark | string | 备注 |
| tagid_list | array | 标签ID列表 |
| subscribe_scene | string | 关注来源（ADD_SCENE_QR_CODE等） |
| qr_scene | number | 二维码场景值 |
| qr_scene_str | string | 二维码场景描述 |
