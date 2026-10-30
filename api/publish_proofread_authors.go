package main

// publish_proofread_authors.go — 原创稿的校对提醒 @ 署名里的作者与编辑（2026-10-30 用户裁定：「这篇文章
// 如果是我们的原创文章……有错误的地方你就直接在底下@他，别@我」）。
//
// 署名写的是笔名（Sage、Bobo、Mote莫特），审核群成员接口只给真名；笔名在各人的飞书资料里（英文名或别名），
// 审核应用读通讯录对上。对不上的——转载稿、原创稿没写署名、署名的人不在群里或资料里没填笔名——照旧 @ 校对负责人；
// 原创稿写了署名却一个都没对上时，提醒里说一句没找到谁。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type proofreadMember struct {
	OpenID string
	Names  []string // 飞书名字、英文名、别名
}

// proofreadBylineRoles 署名里算作者、编辑的角色；设计、审核、英文来源这些不 @。
var proofreadBylineRoles = map[string]bool{
	"作者": true, "撰文": true, "撰稿": true, "文": true, "采访": true, "编译": true, "翻译": true,
	"编辑": true, "责编": true, "责任编辑": true, "新媒体编辑": true, "中文编辑": true,
}

var proofreadNameSplit = regexp.MustCompile(`[、/／&＆;；\s]+`)

// proofreadBylineNames 原创稿署名（开头几块与正文最后，同外发 takeCredits）里的作者与编辑，按原文顺序去重；
// 转载稿、没有署名时为空。
func proofreadBylineNames(job publishJob) []string {
	if original, _ := publishArticleProvenance(job); !original {
		return nil
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err != nil {
		return nil
	}
	_, credits := takeCredits(blocks)
	seen := map[string]bool{}
	var names []string
	for _, credit := range credits {
		role, value, ok := strings.Cut(credit, "：")
		if !ok || !proofreadBylineRoles[role] {
			continue
		}
		for _, name := range proofreadNameSplit.Split(value, -1) {
			if key := proofreadNameKey(name); key != "" && !seen[key] {
				seen[key] = true
				names = append(names, strings.TrimSpace(name))
			}
		}
	}
	return names
}

func proofreadNameKey(s string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '·' || r == '・' {
			return -1
		}
		return r
	}, s))
}

// proofreadNameParts 「Mote莫特」拆成「mote」「莫特」：笔名常是英文名连着中文名，署名和资料可能只写了一半。
// 一个字的半边不算，免得误认。
func proofreadNameParts(key string) []string {
	var latin, other strings.Builder
	for _, r := range key {
		if r <= unicode.MaxASCII {
			latin.WriteRune(r)
		} else {
			other.WriteRune(r)
		}
	}
	if latin.Len() == 0 || other.Len() == 0 {
		return nil
	}
	var parts []string
	for _, part := range []string{latin.String(), other.String()} {
		if len([]rune(part)) >= 2 {
			parts = append(parts, part)
		}
	}
	return parts
}

// matchProofreadMembers 每个署名对到唯一一个群成员：先比整个名字，没有再比英文、中文半边；对上不止一个人
// 就当没找到，宁可不 @ 也不 @ 错人。
func matchProofreadMembers(names []string, members []proofreadMember) (matched []publishDistributionOwner, missing []string) {
	seen := map[string]bool{}
	for _, name := range names {
		key := proofreadNameKey(name)
		hit := proofreadMembersWith(members, func(member string) bool { return member == key })
		if len(hit) == 0 {
			want := map[string]bool{key: true}
			for _, part := range proofreadNameParts(key) {
				want[part] = true
			}
			hit = proofreadMembersWith(members, func(member string) bool {
				if want[member] {
					return true
				}
				for _, part := range proofreadNameParts(member) {
					if want[part] {
						return true
					}
				}
				return false
			})
		}
		if len(hit) != 1 {
			missing = append(missing, name)
			continue
		}
		if !seen[hit[0].OpenID] {
			seen[hit[0].OpenID] = true
			matched = append(matched, publishDistributionOwner{OpenID: hit[0].OpenID, Name: name})
		}
	}
	return matched, missing
}

func proofreadMembersWith(members []proofreadMember, match func(string) bool) []proofreadMember {
	var out []proofreadMember
	for _, member := range members {
		for _, name := range member.Names {
			if key := proofreadNameKey(name); key != "" && match(key) {
				out = append(out, member)
				break
			}
		}
	}
	return out
}

// proofreadRecipients 这条提醒 @ 谁：原创稿对上署名的人就 @ 他们；否则 @ 校对负责人，原创稿写了署名
// 却没对上时一并返回没找到的名字。
func (p *publishPipeline) proofreadRecipients(ctx context.Context, job publishJob, settings publishDeliverySettings) ([]publishDistributionOwner, []string, error) {
	names := proofreadBylineNames(job)
	if len(names) == 0 {
		return p.publishProofreadOwners(settings.AppProfileID), nil, nil
	}
	members, err := p.proofreadReviewMembers(ctx, job, settings)
	if err != nil {
		return nil, nil, err
	}
	matched, missing := matchProofreadMembers(names, members)
	if len(matched) > 0 {
		return matched, nil, nil
	}
	return p.publishProofreadOwners(settings.AppProfileID), missing, nil
}

// proofreadReviewMembers 这篇审核群的全部成员（去重）。
func (p *publishPipeline) proofreadReviewMembers(ctx context.Context, job publishJob, settings publishDeliverySettings) ([]proofreadMember, error) {
	seen := map[string]bool{}
	var out []proofreadMember
	for _, chatID := range settings.ChatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		var members []proofreadMember
		var err error
		if p.proofreadMemberLister != nil {
			members, err = p.proofreadMemberLister(ctx, settings.AppProfileID, chatID)
		} else {
			members, err = p.readProofreadMembers(ctx, job, chatID)
		}
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			if !seen[member.OpenID] {
				seen[member.OpenID] = true
				out = append(out, member)
			}
		}
	}
	return out, nil
}

// readProofreadMembers 审核应用读群成员和各人的飞书资料（笔名在别名里）。谁的资料读不到（不在通讯录权限范围，
// 如生产群里的「用户633772」）就只用他的真名，别人照读；笔名对不上就照旧 @ 校对负责人，不挡提醒。
func (p *publishPipeline) readProofreadMembers(ctx context.Context, job publishJob, chatID string) ([]proofreadMember, error) {
	settings, apps, err := p.reviewDeliveryForJob(job)
	if err != nil {
		return nil, err
	}
	notifier, err := p.configuredReviewNotifier(settings, apps, chatID)
	if err != nil {
		return nil, err
	}
	list, err := notifier.listChatMembers(ctx, chatID)
	if err != nil {
		return nil, err
	}
	out := make([]proofreadMember, 0, len(list))
	for _, member := range list {
		names := []string{member.Name}
		extra, err := notifier.userProfileNames(ctx, member.OpenID)
		if err != nil {
			log.Printf("校对提醒：读不到审核群成员 %s 的飞书资料，只用真名：%v", member.Name, err)
		}
		out = append(out, proofreadMember{OpenID: member.OpenID, Names: append(names, extra...)})
	}
	return out, nil
}

// userProfileNames 一个人飞书资料里的英文名与别名（笔名填在这里）；要审核应用开通读通讯录的权限。
func (n *feishuNotifier) userProfileNames(ctx context.Context, openID string) ([]string, error) {
	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		n.baseURL+"/open-apis/contact/v3/users/"+url.PathEscape(openID)+"?user_id_type=open_id", nil)
	if err != nil {
		return nil, fmt.Errorf("feishu: 构造用户资料请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("飞书用户资料接口不可达: %w", err)
	}
	body, err := readBoundedBody(resp.Body, 1<<20)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("飞书用户资料响应读取失败: %w", err)
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			User struct {
				EnName   string `json:"en_name"`
				Nickname string `json:"nickname"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("飞书用户资料响应异常（HTTP %d）: %w", resp.StatusCode, err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("读取飞书用户资料失败 code=%d msg=%s", env.Code, env.Msg)
	}
	var names []string
	for _, name := range []string{env.Data.User.EnName, env.Data.User.Nickname} {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}
