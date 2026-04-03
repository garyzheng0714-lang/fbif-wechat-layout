# 发布草稿

**接口英文名**：freepublish_submit

将草稿提交发布。调用成功仅表示发布任务提交成功，不代表发布已完成。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/freepublish/submit?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media_id | string | 是 | 要发布的草稿 media_id |

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| errcode | number | 错误码 |
| errmsg | string | 错误信息 |
| publish_id | string | 发布任务ID |
| msg_data_id | string | 消息数据ID |

## 返回示例

```json
{
  "errcode": 0,
  "errmsg": "ok",
  "publish_id": "100000001"
}
```

## 发布结果推送

发布结果通过事件推送返回，推送类型为 `PUBLISHJOBFINISH`：
- 0：成功
- 2：原创失败
- 3：常规失败
- 4：平台审核不通过

## 错误码

| 错误码 | 说明 | 解决方案 |
|--------|------|---------|
| 48001 | api unauthorized | 确认公众号已获得该接口权限 |
| 53503 | 草稿未通过发布检查 | 检查草稿信息 |
