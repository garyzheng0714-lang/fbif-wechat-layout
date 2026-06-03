package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	ftFooterDecorImg  = "https://static.foodtalks.cn/image/post/07079a66940e1ff57e71b140e7e36357.webp"
	ftFooterGroupQR   = "https://static.foodtalks.cn/image/news/6e309987660da9af009b89be08c4aa74ed78.png"
	ftFooterAccountQR = "https://static.foodtalks.cn/image/post/b5b448f324a538209eb53379bef8dea9a7bf.jpg"
	ftFooterLogo      = "https://static.foodtalks.cn/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png"
)

var assetLibraryID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func assetRefUsage(usage string) string {
	return "{{asset:" + usage + "}}"
}

func assetLibraryImage(id, usage, label, url, scope string, width, height int, tags ...any) map[string]any {
	out := map[string]any{
		"id":    id,
		"type":  "image",
		"url":   url,
		"scope": scope,
		"usage": usage,
		"label": label,
		"tags":  tags,
	}
	if width > 0 {
		out["width"] = width
	}
	if height > 0 {
		out["height"] = height
	}
	return out
}

func defaultAssetLibraryModule() map[string]any {
	assets := []any{
		assetLibraryImage("ft_footer_banner", "banner", "FoodTalks 页脚装饰图", ftFooterDecorImg, "global", 600, 0, "FoodTalks", "页脚"),
		assetLibraryImage("ft_footer_group_qr", "qr_group", "FoodTalks 加群二维码", ftFooterGroupQR, "global", 600, 267, "二维码", "页脚"),
		assetLibraryImage("ft_footer_account_qr", "qr_account", "FoodTalks 公众号二维码", ftFooterAccountQR, "global", 600, 221, "二维码", "页脚"),
		assetLibraryImage("ft_footer_logo", "logo", "FoodTalks logo", ftFooterLogo, "global", 130, 27, "logo", "页脚"),
		// FBIF 公众号底部素材（footer.html 内联 base64 提取为站内静态文件；复制时自动上传微信 CDN）。
		// 不设 width/height：原 footer.html <img> 仅用 style 控宽，补 width/height 属性会破坏字节级保真。
		assetLibraryImage("fbif_bobo_card", "avatar", "FBIF 波波名片", "/assets/fbif/bobo-card.webp", "fbif", 0, 0, "FBIF", "名片"),
		assetLibraryImage("fbif_promo_1000", "decor", "FBIF 1000个食品创新案例引流图", "/assets/fbif/promo-1000.webp", "fbif", 0, 0, "FBIF", "引流图"),
	}
	assets = append(assets, defaultSeriesAssetLibraryAssets()...)
	return map[string]any{
		"schema_version": 1,
		"assets":         assets,
	}
}

func defaultSeriesAssetLibraryAssets() []any {
	materials := defaultFtSeriesMaterialsModule()
	out := []any{}
	appendURLMap := func(kind, prefix, labelSuffix string) {
		raw, _ := materials[kind].(map[string]any)
		keys := make([]string, 0, len(raw))
		for key := range raw {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if kind == "sections" {
				left, _ := strconv.Atoi(keys[i])
				right, _ := strconv.Atoi(keys[j])
				return left < right
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			url := strings.TrimSpace(fmt.Sprint(raw[key]))
			if url == "" {
				continue
			}
			idPart := assetIDPartFromURL(url)
			label := fmt.Sprintf("什么是系列%s%s", key, labelSuffix)
			if kind == "sections" {
				num, _ := strconv.Atoi(key)
				idPart = fmt.Sprintf("%02d", num)
				label = fmt.Sprintf("什么是系列序号图 %02d", num)
			}
			usage := "custom"
			if kind == "cards" {
				usage = "avatar"
			}
			out = append(out, assetLibraryImage("ft_series_"+prefix+"_"+idPart, usage, label, url, "foodtalks-series", 0, 0, "什么是系列", labelSuffix))
		}
	}
	appendURLMap("sections", "section", "序号图")
	appendURLMap("groups", "group", "群二维码")
	appendURLMap("cards", "card", "名片")
	return out
}

func assetIDPartFromURL(rawURL string) string {
	base := rawURL
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	base = strings.ToLower(base)
	base = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if base == "" {
		return "asset"
	}
	return base
}

// validateAssetLibraryModule 校验通用素材库（config.modules.asset_library）。
// 素材元数据跟随 /api/rules 保存；图片字节仍走现有 /api/oss-upload，不在这里引入 OSS SDK。
func validateAssetLibraryModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("asset_library module must be an object")
	}
	allowed := map[string]bool{"schema_version": true, "assets": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown asset_library field: %s", k)
		}
	}
	if version, ok := m["schema_version"]; ok && int(ruleBehaviorNumber(version)) != 1 {
		return fmt.Errorf("unsupported asset_library schema_version")
	}
	rawAssets, ok := m["assets"]
	if !ok {
		return nil
	}
	assets, ok := rawAssets.([]any)
	if !ok {
		return fmt.Errorf("asset_library assets must be an array")
	}
	if len(assets) > 500 {
		return fmt.Errorf("asset_library has too many assets")
	}
	ids := map[string]bool{}
	for i, rawAsset := range assets {
		asset, ok := rawAsset.(map[string]any)
		if !ok {
			return fmt.Errorf("asset_library assets[%d] must be an object", i)
		}
		if err := validateAssetLibraryAsset(i, asset, ids); err != nil {
			return err
		}
	}
	return nil
}

func validateAssetLibraryAsset(index int, asset map[string]any, ids map[string]bool) error {
	allowed := map[string]bool{
		"id": true, "type": true, "url": true, "scope": true, "usage": true,
		"label": true, "tags": true, "width": true, "height": true, "updated_at": true,
	}
	for k := range asset {
		if !allowed[k] {
			return fmt.Errorf("unknown asset_library assets[%d] field: %s", index, k)
		}
	}
	id := strings.TrimSpace(fmt.Sprint(asset["id"]))
	if !assetLibraryID.MatchString(id) {
		return fmt.Errorf("asset_library assets[%d].id is invalid", index)
	}
	if ids[id] {
		return fmt.Errorf("duplicate asset_library asset id: %s", id)
	}
	ids[id] = true
	typ := strings.TrimSpace(fmt.Sprint(asset["type"]))
	if typ != "image" {
		return fmt.Errorf("asset_library assets[%d].type is unsupported", index)
	}
	url := strings.TrimSpace(fmt.Sprint(asset["url"]))
	if !isSafeRuleBehaviorURL(url) {
		return fmt.Errorf("asset_library assets[%d].url must be a site path or HTTPS URL", index)
	}
	scope := strings.TrimSpace(fmt.Sprint(asset["scope"]))
	if !isAllowedAssetScope(scope) {
		return fmt.Errorf("asset_library assets[%d].scope is invalid", index)
	}
	usage := strings.TrimSpace(fmt.Sprint(asset["usage"]))
	if !isAllowedAssetUsage(usage) {
		return fmt.Errorf("asset_library assets[%d].usage is invalid", index)
	}
	for _, key := range []string{"width", "height"} {
		if raw, ok := asset[key]; ok && raw != nil {
			n := ruleBehaviorNumber(raw)
			if n < 0 || n > 5000 {
				return fmt.Errorf("asset_library assets[%d].%s out of range", index, key)
			}
		}
	}
	if label, ok := asset["label"]; ok && len([]rune(fmt.Sprint(label))) > 80 {
		return fmt.Errorf("asset_library assets[%d].label too long", index)
	}
	if updatedAt, ok := asset["updated_at"]; ok && len([]rune(fmt.Sprint(updatedAt))) > 80 {
		return fmt.Errorf("asset_library assets[%d].updated_at too long", index)
	}
	if tags, ok := asset["tags"]; ok {
		items, ok := tags.([]any)
		if !ok {
			return fmt.Errorf("asset_library assets[%d].tags must be an array", index)
		}
		if len(items) > 20 {
			return fmt.Errorf("asset_library assets[%d].tags has too many items", index)
		}
		for tagIndex, item := range items {
			if len([]rune(fmt.Sprint(item))) > 30 {
				return fmt.Errorf("asset_library assets[%d].tags[%d] too long", index, tagIndex)
			}
		}
	}
	return nil
}

func isAllowedAssetUsage(usage string) bool {
	switch usage {
	case "qr_group", "qr_account", "logo", "banner", "avatar", "decor", "custom":
		return true
	default:
		return false
	}
}

func isAllowedAssetScope(scope string) bool {
	if scope == "global" {
		return true
	}
	for _, account := range accountCatalog() {
		if scope == account.ID {
			return true
		}
	}
	return false
}
