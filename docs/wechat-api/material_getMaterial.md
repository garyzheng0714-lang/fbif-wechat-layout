# 获取永久素材

**接口英文名**：getMaterial

根据 media_id 获取永久素材的详细信息。

## 调用方式

```
POST https://api.weixin.qq.com/cgi-bin/material/get_material?access_token=ACCESS_TOKEN
```

## 请求参数（JSON Body）

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| media_id | string | 是 | 素材 media_id |

## 返回结构

返回结构根据素材类型不同：

### 图文素材

返回 `news_item` 数组，包含：title、author、digest、content、thumb_media_id、show_cover_pic、url、content_source_url

### 视频素材

返回：title、description、down_url

### 其他类型

直接返回素材二进制内容。

## 错误码

| 错误码 | 描述 | 解决方案 |
|--------|------|---------|
| -1 | 系统错误 | 稍后重试 |
| 40001 | invalid access_token | 检查 AppSecret |
| 40007 | invalid media_id | 检查 media_id |
