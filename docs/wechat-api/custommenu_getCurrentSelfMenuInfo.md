# 查询当前菜单信息

**接口英文名**：getCurrentSelfmenuInfo

获取当前菜单配置，包括通过 API 和官网创建的菜单。

## 调用方式

```
GET https://api.weixin.qq.com/cgi-bin/get_current_selfmenu_info?access_token=ACCESS_TOKEN
```

## 返回参数

| 参数名 | 类型 | 说明 |
|--------|------|------|
| is_menu_open | number | 菜单是否启用（0=关，1=开） |
| selfmenu_info.button | array | 菜单按钮数组 |

每个 button 包含：type、name、key、url、value、news_info 等。
