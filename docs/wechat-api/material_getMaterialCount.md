# 获取永久素材总数

**接口英文名**：getMaterialCount

获取各类永久素材的数量统计。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/material/get_materialcount?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| voice_count | number | 语音素材总数 |
| video_count | number | 视频素材总数 |
| image_count | number | 图片素材总数 |
| news_count | number | 图文素材总数 |

## 返回示例

```json
{
  "voice_count": 5,
  "video_count": 3,
  "image_count": 10,
  "news_count": 7
}
```

## 存储上限

- 图片和图文素材：100,000
- 其他素材：1,000
