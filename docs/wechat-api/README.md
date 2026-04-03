# 微信公众号 API 文档

来源：https://developers.weixin.qq.com/doc/subscription/api/

## 基础接口

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [base_getAccessToken](base_getAccessToken.md) | 获取access_token | GET | /cgi-bin/token |
| [base_getStableAccessToken](base_getStableAccessToken.md) | 获取稳定版access_token | POST | /cgi-bin/stable_token |
| [base_callbackCheck](base_callbackCheck.md) | 网络通信检测 | POST | /cgi-bin/callback/check |
| [base_getApiDomainIP](base_getApiDomainIP.md) | 获取API服务器IP | GET | /cgi-bin/get_api_domain_ip |
| [base_getCallbackIP](base_getCallbackIP.md) | 获取推送服务器IP | GET | /cgi-bin/getcallbackip |

## OpenAPI 管理

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [apimanage_clearQuota](apimanage_clearQuota.md) | 重置API调用次数 | POST | /cgi-bin/clear_quota |
| [apimanage_getApiQuota](apimanage_getApiQuota.md) | 查询API调用额度 | POST | /cgi-bin/openapi/quota/get |
| [apimanage_getRidInfo](apimanage_getRidInfo.md) | 查询RID信息 | POST | /cgi-bin/openapi/rid/get |
| [apimanage_clearQuotaByAppSecret](apimanage_clearQuotaByAppSecret.md) | 用AppSecret重置次数 | POST | /cgi-bin/clear_quota/v2 |
| [apimanage_clearApiQuota](apimanage_clearApiQuota.md) | 重置指定API次数 | POST | /cgi-bin/openapi/quota/clear |

## 草稿箱管理 ⭐

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [draft_add](draft_add.md) | **新增草稿** | POST | /cgi-bin/draft/add |
| [draft_update](draft_update.md) | 更新草稿 | POST | /cgi-bin/draft/update |
| [draft_batchget](draft_batchget.md) | 获取草稿列表 | POST | /cgi-bin/draft/batchget |
| [draft_count](draft_count.md) | 获取草稿总数 | GET | /cgi-bin/draft/count |
| [draft_delete](draft_delete.md) | 删除草稿 | POST | /cgi-bin/draft/delete |
| [draft_switch](draft_switch.md) | 草稿箱开关 | POST | /cgi-bin/draft/switch |

## 发布管理

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [freepublish_submit](freepublish_submit.md) | 发布草稿 | POST | /cgi-bin/freepublish/submit |

## 素材管理 ⭐

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [media_uploadimg](media_uploadimg.md) | **上传内容图片** | POST | /cgi-bin/media/uploadimg |
| [material_addMaterial](material_addMaterial.md) | 上传永久素材 | POST | /cgi-bin/material/add_material |
| [material_getMaterial](material_getMaterial.md) | 获取永久素材 | POST | /cgi-bin/material/get_material |
| [material_getMaterialCount](material_getMaterialCount.md) | 获取素材总数 | GET | /cgi-bin/material/get_materialcount |
| [material_batchGetMaterial](material_batchGetMaterial.md) | 获取素材列表 | POST | /cgi-bin/material/batchget_material |
| [material_delMaterial](material_delMaterial.md) | 删除永久素材 | POST | /cgi-bin/material/del_material |
| [media_uploadTempMedia](media_uploadTempMedia.md) | 上传临时素材 | POST | /cgi-bin/media/upload |
| [media_getTempMedia](media_getTempMedia.md) | 获取临时素材 | GET | /cgi-bin/media/get |
| [media_getHDVoice](media_getHDVoice.md) | 获取高清语音 | GET | /cgi-bin/media/get/jssdk |
| [media_uploadNews](media_uploadNews.md) | 上传图文素材（废弃） | POST | /cgi-bin/media/uploadnews |

## 自定义菜单

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [custommenu_create](custommenu_create.md) | 创建菜单 | POST | /cgi-bin/menu/create |
| [custommenu_getMenu](custommenu_getMenu.md) | 获取菜单配置 | GET | /cgi-bin/menu/get |
| [custommenu_getCurrentSelfMenuInfo](custommenu_getCurrentSelfMenuInfo.md) | 查询当前菜单 | GET | /cgi-bin/get_current_selfmenu_info |
| [custommenu_delete](custommenu_delete.md) | 删除菜单 | GET | /cgi-bin/menu/delete |
| [custommenu_addConditional](custommenu_addConditional.md) | 创建个性化菜单 | POST | /cgi-bin/menu/addconditional |
| [custommenu_deleteConditional](custommenu_deleteConditional.md) | 删除个性化菜单 | POST | /cgi-bin/menu/delconditional |
| [custommenu_tryMatch](custommenu_tryMatch.md) | 测试菜单匹配 | POST | /cgi-bin/menu/trymatch |

## 消息管理

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [message_massSendAll](message_massSendAll.md) | 按标签群发 | POST | /cgi-bin/message/mass/sendall |
| [message_sendCustomMessage](message_sendCustomMessage.md) | 发送客服消息 | POST | /cgi-bin/message/custom/send |

## 用户管理

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [user_getUserInfo](user_getUserInfo.md) | 获取用户信息 | GET | /cgi-bin/user/info |
| [user_getTags](user_getTags.md) | 获取标签列表 | GET | /cgi-bin/tags/get |

## 网页开发

| 文件 | 接口 | 方法 | 路径 |
|------|------|------|------|
| [webdev_getTicket](webdev_getTicket.md) | 获取JS-SDK ticket | GET | /cgi-bin/ticket/getticket |

---

## 推草稿箱的核心调用流程

```
1. 获取 access_token
   GET /cgi-bin/token?grant_type=client_credential&appid=APPID&secret=SECRET

2. 上传正文中的图片到微信CDN
   POST /cgi-bin/media/uploadimg  →  返回 mmbiz.qpic.cn URL

3. 上传封面图为永久素材
   POST /cgi-bin/material/add_material?type=image  →  返回 media_id

4. 创建草稿
   POST /cgi-bin/draft/add
   {
     "articles": [{
       "title": "标题",
       "content": "<p>正文，图片用步骤2的URL</p>",
       "thumb_media_id": "步骤3的media_id"
     }]
   }
   → 返回 media_id（草稿ID）

5.（可选）发布
   POST /cgi-bin/freepublish/submit  { "media_id": "草稿ID" }
```
