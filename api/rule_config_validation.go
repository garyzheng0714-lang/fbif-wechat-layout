package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type ruleNumberField struct {
	min float64
	max float64
}

var ruleNumberFields = map[string]ruleNumberField{
	"font_size":                {min: 12, max: 24},
	"heading_size":             {min: 14, max: 32},
	"line_height":              {min: 1, max: 3},
	"letter_spacing":           {min: 0, max: 0.2},
	"paragraph_margin_x":       {min: 0, max: 28},
	"paragraph_gap":            {min: 0, max: 48},
	"caption_font_size":        {min: 10, max: 18},
	"author_font_size":         {min: 10, max: 20},
	"tip_font_size":            {min: 10, max: 20},
	"attribution_font_size":    {min: 10, max: 20},
	"footer_note_font_size":    {min: 10, max: 20}, // 系列文末署名/图源说明小字（红线下放）
	"blockquote_padding_left":  {min: 0, max: 32},
	"blockquote_border_width":  {min: 0, max: 8},
	"md_heading_max_chars":     {min: 10, max: 120},
	"decorative_image_max_px":  {min: 120, max: 1000},
	"more_articles_slots":      {min: 0, max: 8},
	"unpublish_window_hours":   {min: 0, max: 720},
	"banner_overlay_alpha":     {min: 0, max: 1},
	"banner_title_x":           {min: 0, max: 400},
	"banner_title_y":           {min: 0, max: 220},
	"banner_title_width":       {min: 300, max: 1000},
	"banner_title_box_height":  {min: 40, max: 220},
	"banner_title_font_size":   {min: 24, max: 80},
	"banner_title_line_height": {min: 32, max: 100},
	"banner_title_max_lines":   {min: 1, max: 4},
	"table_font_size":          {min: 12, max: 24},
	"table_cell_padding":       {min: 0, max: 24},
	// FoodTalks「什么是」系列专属字号（红线下放：规则后台可配）
	"h1_font_size":   {min: 14, max: 32},
	"h2_font_size":   {min: 12, max: 24},
	"toc_title_size": {min: 12, max: 24},
	"image_margin_x": {min: 0, max: 28}, // 系列模板图片两端缩进（FoodTalks食品资讯与正文同为 8）
}

var ruleColorFields = map[string]bool{
	"text_color":              true,
	"link_color":              true,
	"muted_color":             true,
	"tip_title_color":         true, // 网站提示标题色（红线下放）
	"tip_body_color":          true, // 网站提示正文色（红线下放）
	"table_header_bg":         true, // 表头底色（统一表格样式，红线下放）
	"table_header_text_color": true, // 表头文字色
	"table_border_color":      true, // 表格边框色
	"heading_color":           true, // FoodTalks「什么是」系列标题/链接主色（红线下放）
	"footer_note_color":       true, // 系列文末署名/图源说明小字色（红线下放）
}

var ruleBooleanFields = map[string]bool{
	"footer_enabled":        true,
	"skip_upload":           true,
	"more_articles_enabled": true,
	"table_header_bold":     true, // 表头加粗（统一表格样式，红线下放）
	"justify_paragraphs":    true, // 段落/标题两端对齐：ft-default=true(FBIF搬运文口径)；网站系列=false(成品裸标签)
}

var ruleTextFields = map[string]bool{
	"font_family": true,
}

var ruleStructuredFields = map[string]bool{
	"more_articles_default_cards": true,
}

var ruleModuleOrder = []string{"theme", "supporting", "more_articles", "table", "workflow"}

var ruleModuleFields = map[string]map[string]bool{
	"theme": {
		"font_size":          true,
		"heading_size":       true,
		"line_height":        true,
		"letter_spacing":     true,
		"paragraph_margin_x": true,
		"paragraph_gap":      true,
		"font_family":        true,
		"text_color":         true,
		"link_color":         true,
		"muted_color":        true,
		"justify_paragraphs": true,
		"tip_title_color":    true,
		"tip_body_color":     true,
	},
	"supporting": {
		"caption_font_size":       true,
		"author_font_size":        true,
		"tip_font_size":           true,
		"attribution_font_size":   true,
		"blockquote_padding_left": true,
		"blockquote_border_width": true,
		"md_heading_max_chars":    true,
		"decorative_image_max_px": true,
	},
	"more_articles": {
		"more_articles_enabled":       true,
		"more_articles_slots":         true,
		"more_articles_default_cards": true,
		"banner_overlay_alpha":        true,
		"banner_title_x":              true,
		"banner_title_y":              true,
		"banner_title_width":          true,
		"banner_title_box_height":     true,
		"banner_title_font_size":      true,
		"banner_title_line_height":    true,
		"banner_title_max_lines":      true,
	},
	"table": {
		"table_header_bg":         true,
		"table_header_text_color": true,
		"table_header_bold":       true,
		"table_border_color":      true,
		"table_font_size":         true,
		"table_cell_padding":      true,
	},
	"workflow": {
		"footer_enabled":         true,
		"skip_upload":            true,
		"unpublish_window_hours": true,
	},
}

var ruleHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var ruleFontFamily = regexp.MustCompile(`^[A-Za-z0-9一-龥 -]{1,40}$`)
var behaviorUnsafeTemplate = regexp.MustCompile(`(?i)<script|on\w+\s*=|javascript:`)
var behaviorUnsafeURL = regexp.MustCompile(`[\x00-\x20\x7F"'<>\\]`)

type browserRegexAtom struct {
	valid             bool
	group             bool
	wildcard          bool
	hasVariableRepeat bool
	hasAlternation    bool
}

type browserRegexFrame struct {
	hasVariableRepeat bool
	hasAlternation    bool
	hasWildcard       bool
	last              browserRegexAtom
}

type browserRegexQuantifier struct {
	variable  bool
	unbounded bool
	max       int
}

func isRuleBehaviorRegexKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "regex") || strings.Contains(lower, "pattern") ||
		strings.Contains(lower, "marker") || strings.Contains(lower, "signature")
}

// validateBrowserSafeRegex keeps server-side RE2 validation aligned with the
// smaller regex subset that is safe to execute in JavaScript's backtracking
// engine. Runtime code also caps inspected text, but unsafe structures are
// rejected here so a saved rule cannot turn into a browser-side ReDoS payload.
func validateBrowserSafeRegex(path, source string, maxRunes int) error {
	if len([]rune(source)) > maxRunes {
		return fmt.Errorf("%s is too long", path)
	}
	if _, err := regexp.Compile(source); err != nil {
		return fmt.Errorf("%s is not a valid regex", path)
	}
	if reason := browserUnsafeRegexReason(source); reason != "" {
		return fmt.Errorf("%s is not browser-safe: %s", path, reason)
	}
	return nil
}

func stripBrowserRegexInlineFlags(source string) (string, string) {
	if !strings.HasPrefix(source, "(?") || len(source) < 4 || source[2] < 'a' || source[2] > 'z' {
		return source, ""
	}
	close := strings.IndexByte(source, ')')
	if close < 3 {
		return source, ""
	}
	flags := source[2:close]
	for _, flag := range flags {
		if flag != 'i' && flag != 'm' && flag != 's' {
			return source, "only a leading (?i), (?m), or (?s) flag group is supported"
		}
	}
	return source[close+1:], ""
}

func parseBrowserRegexBraceQuantifier(source string, start int) (browserRegexQuantifier, int, bool) {
	i := start + 1
	minStart := i
	for i < len(source) && source[i] >= '0' && source[i] <= '9' {
		i++
	}
	if i == minStart {
		return browserRegexQuantifier{}, start, false
	}
	min, err := strconv.Atoi(source[minStart:i])
	if err != nil {
		return browserRegexQuantifier{}, start, false
	}
	if i < len(source) && source[i] == '}' {
		return browserRegexQuantifier{max: min}, i + 1, true
	}
	if i >= len(source) || source[i] != ',' {
		return browserRegexQuantifier{}, start, false
	}
	i++
	maxStart := i
	for i < len(source) && source[i] >= '0' && source[i] <= '9' {
		i++
	}
	if i >= len(source) || source[i] != '}' {
		return browserRegexQuantifier{}, start, false
	}
	if i == maxStart {
		return browserRegexQuantifier{variable: true, unbounded: true}, i + 1, true
	}
	max, err := strconv.Atoi(source[maxStart:i])
	if err != nil {
		return browserRegexQuantifier{}, start, false
	}
	return browserRegexQuantifier{variable: min != max, max: max}, i + 1, true
}

func isBrowserRegexWildcardClass(source string) bool {
	return strings.HasPrefix(source, "[^") ||
		(strings.Contains(source, `\s`) && strings.Contains(source, `\S`)) ||
		(strings.Contains(source, `\d`) && strings.Contains(source, `\D`)) ||
		(strings.Contains(source, `\w`) && strings.Contains(source, `\W`))
}

func applyBrowserRegexQuantifier(frame *browserRegexFrame, quantifier browserRegexQuantifier, unboundedWildcards *int) string {
	if !frame.last.valid {
		return "a quantifier must follow an atom"
	}
	repeatsMoreThanOnce := quantifier.unbounded || quantifier.max > 1
	if frame.last.group && repeatsMoreThanOnce && (frame.last.hasVariableRepeat || frame.last.hasAlternation) {
		return "nested or ambiguous repetition is not allowed"
	}
	if frame.last.wildcard && quantifier.unbounded {
		*unboundedWildcards++
		if *unboundedWildcards > 1 {
			return "multiple unbounded wildcard repetitions are not allowed"
		}
	}
	if quantifier.variable {
		frame.hasVariableRepeat = true
	}
	return ""
}

func browserUnsafeRegexReason(source string) string {
	var reason string
	source, reason = stripBrowserRegexInlineFlags(source)
	if reason != "" {
		return reason
	}
	frames := []browserRegexFrame{{}}
	unboundedWildcards := 0
	for i := 0; i < len(source); {
		frame := &frames[len(frames)-1]
		switch source[i] {
		case '\\':
			if i+1 >= len(source) {
				return "a trailing escape is not allowed"
			}
			if (source[i+1] >= '1' && source[i+1] <= '9') || source[i+1] == 'k' {
				return "backreferences are not supported"
			}
			frame.last = browserRegexAtom{valid: true}
			i += 2
		case '[':
			end := i + 1
			for end < len(source) {
				if source[end] == '\\' {
					end += 2
					continue
				}
				if source[end] == ']' {
					break
				}
				end++
			}
			if end >= len(source) {
				return "an unterminated character class is not allowed"
			}
			wildcard := isBrowserRegexWildcardClass(source[i : end+1])
			frame.hasWildcard = frame.hasWildcard || wildcard
			frame.last = browserRegexAtom{valid: true, wildcard: wildcard}
			i = end + 1
		case '(':
			if i+1 < len(source) && source[i+1] == '?' {
				if i+2 >= len(source) || source[i+2] != ':' {
					return "only non-capturing (?:...) groups are supported after (?"
				}
				i += 3
			} else {
				i++
			}
			frames = append(frames, browserRegexFrame{})
		case ')':
			if len(frames) == 1 {
				return "an unmatched closing group is not allowed"
			}
			closed := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			parent := &frames[len(frames)-1]
			parent.hasVariableRepeat = parent.hasVariableRepeat || closed.hasVariableRepeat
			parent.hasAlternation = parent.hasAlternation || closed.hasAlternation
			parent.hasWildcard = parent.hasWildcard || closed.hasWildcard
			parent.last = browserRegexAtom{
				valid:             true,
				group:             true,
				wildcard:          closed.hasWildcard,
				hasVariableRepeat: closed.hasVariableRepeat,
				hasAlternation:    closed.hasAlternation,
			}
			i++
		case '|':
			frame.hasAlternation = true
			frame.last = browserRegexAtom{}
			i++
		case '*', '+', '?':
			quantifier := browserRegexQuantifier{variable: true, max: 1}
			if source[i] == '*' || source[i] == '+' {
				quantifier.unbounded = true
			}
			if reason := applyBrowserRegexQuantifier(frame, quantifier, &unboundedWildcards); reason != "" {
				return reason
			}
			i++
			if i < len(source) && source[i] == '?' {
				i++ // lazy suffix; it does not change the repetition bound
			}
		case '{':
			quantifier, end, ok := parseBrowserRegexBraceQuantifier(source, i)
			if !ok {
				frame.last = browserRegexAtom{valid: true}
				i++
				continue
			}
			if reason := applyBrowserRegexQuantifier(frame, quantifier, &unboundedWildcards); reason != "" {
				return reason
			}
			i = end
			if i < len(source) && source[i] == '?' {
				i++
			}
		case '.':
			frame.hasWildcard = true
			frame.last = browserRegexAtom{valid: true, wildcard: true}
			i++
		case '^', '$':
			frame.last = browserRegexAtom{}
			i++
		default:
			frame.last = browserRegexAtom{valid: true}
			i++
		}
	}
	if len(frames) != 1 {
		return "an unterminated group is not allowed"
	}
	return ""
}

func validateRuleConfig(config map[string]any) error {
	if err := validateRuleConfigKeys(config); err != nil {
		return err
	}
	for key, value := range flattenRuleConfig(config) {
		if field, ok := ruleNumberFields[key]; ok {
			n, err := ruleValueAsFloat(value)
			if err != nil {
				return fmt.Errorf("%s must be a number", key)
			}
			if n < field.min || n > field.max {
				return fmt.Errorf("%s out of range", key)
			}
			continue
		}
		if ruleColorFields[key] {
			s, ok := value.(string)
			if !ok || !ruleHexColor.MatchString(strings.TrimSpace(s)) {
				return fmt.Errorf("%s must be a hex color", key)
			}
			continue
		}
		if ruleBooleanFields[key] {
			if _, ok := value.(bool); ok {
				continue
			}
			if s, ok := value.(string); ok && (s == "true" || s == "false") {
				continue
			}
			return fmt.Errorf("%s must be a boolean", key)
		}
		if ruleTextFields[key] {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text == "" {
				return fmt.Errorf("%s must not be empty", key)
			}
			if key == "font_family" && !ruleFontFamily.MatchString(text) {
				return fmt.Errorf("%s contains invalid characters", key)
			}
		}
		if ruleStructuredFields[key] {
			if err := validateRuleStructuredField(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRulePresetPayload(p rulePreset) error {
	if len([]rune(p.Name)) > 80 {
		return fmt.Errorf("name is too long")
	}
	if len([]rune(p.Description)) > 500 {
		return fmt.Errorf("description is too long")
	}
	baselineID := sanitizeRulePresetID(p.BaselineID)
	if isProtectedDefaultPreset(sanitizeRulePresetID(p.ID)) && baselineID == "" {
		baselineID = sanitizeRulePresetID(p.ID)
	}
	if err := validateRuleBaselineID(baselineID); err != nil {
		return err
	}
	if isProtectedDefaultPreset(sanitizeRulePresetID(p.ID)) && baselineID != sanitizeRulePresetID(p.ID) {
		return fmt.Errorf("内置规则 %s 的 baseline_id 必须指向自身", p.ID)
	}
	if err := validateRuleConfig(p.Config); err != nil {
		return err
	}
	return validateRuleBehaviorPayload(baselineID, p.Behavior)
}

func validateRuleBehaviorPayload(baselineID string, raw map[string]any) error {
	regexFields := map[string]bool{"author_regex": true, "tip_regex": true, "ref_header_regex": true, "ref_item_regex": true}
	for key := range raw {
		switch key {
		case "schema_version", "params", "conditions", "actions", "templates", "output_contracts", "lists":
		default:
			if !regexFields[key] {
				return fmt.Errorf("unknown behavior field: %s", key)
			}
		}
	}
	// behavior.lists：网站识别词表/标记列表（红线下放）。每项 []string；
	// 名含 marker/signature/regex/pattern 的项同时校验浏览器安全子集；全部做 XSS 防护与长度上限。
	if err := validateRuleBehaviorLists(raw["lists"]); err != nil {
		return err
	}
	// FoodTalks 网站 behavior 的识别正则（作者/提示/参考来源行）——红线：规则后台可配。
	// 校验为长度受限、RE2/JavaScript 语义兼容且不会触发典型回溯爆炸的安全正则。
	for key := range regexFields {
		if v, ok := raw[key]; ok {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("behavior %s must be a string", key)
			}
			if err := validateBrowserSafeRegex("behavior."+key, s, 200); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"params", "templates", "output_contracts"} {
		if value, ok := raw[key]; ok {
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("behavior %s must be an object", key)
			}
		}
	}
	for _, key := range []string{"conditions", "actions"} {
		if value, ok := raw[key]; ok {
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("behavior %s must be an array", key)
			}
		}
	}
	if version, ok := raw["schema_version"]; ok && int(ruleBehaviorNumber(version)) != 1 {
		return fmt.Errorf("unsupported behavior schema_version")
	}
	behavior := mergeRuleBehavior(baselineID, raw)
	params, ok := behavior["params"].(map[string]any)
	if !ok {
		return fmt.Errorf("behavior params must be an object")
	}
	for key, value := range params {
		spec, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("behavior param %s must be an object", key)
		}
		if err := validateRuleBehaviorParam(key, spec); err != nil {
			return err
		}
	}
	conditionIDs, err := validateRuleBehaviorConditions(behavior["conditions"])
	if err != nil {
		return err
	}
	if err := validateRuleBehaviorActions(behavior["actions"], conditionIDs, params); err != nil {
		return err
	}
	templates, ok := behavior["templates"].(map[string]any)
	if !ok {
		return fmt.Errorf("behavior templates must be an object")
	}
	contracts, _ := behavior["output_contracts"].(map[string]any)
	for key, value := range templates {
		template, ok := value.(string)
		if !ok {
			return fmt.Errorf("behavior template %s must be a string", key)
		}
		maxLength := 2000
		if contract, ok := contracts[key].(map[string]any); ok {
			if n := int(ruleBehaviorNumber(contract["max_length"])); n > 0 {
				maxLength = n
			}
			if allowHTML, ok := contract["allow_html"].(bool); ok && allowHTML {
				return fmt.Errorf("behavior template %s cannot allow raw html", key)
			}
		}
		if len([]rune(template)) > maxLength || len([]rune(template)) > 2000 {
			return fmt.Errorf("behavior template %s is too long", key)
		}
		if behaviorUnsafeTemplate.MatchString(template) {
			return fmt.Errorf("behavior template %s contains unsafe HTML", key)
		}
	}
	for key, value := range contracts {
		contract, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("behavior output contract %s must be an object", key)
		}
		if n := int(ruleBehaviorNumber(contract["max_length"])); n < 0 || n > 2000 {
			return fmt.Errorf("behavior output contract %s has invalid max_length", key)
		}
	}
	return nil
}

func validateRuleBehaviorParam(key string, spec map[string]any) error {
	typ, _ := spec["type"].(string)
	switch typ {
	case "url", "string", "boolean", "number":
	default:
		return fmt.Errorf("unknown behavior param type: %s", key)
	}
	required, _ := spec["required"].(bool)
	defaultValue := ""
	if rawDefault, ok := spec["default"]; ok && rawDefault != nil {
		defaultValue = strings.TrimSpace(fmt.Sprint(rawDefault))
	}
	if required && defaultValue == "" {
		return fmt.Errorf("missing required behavior param default: %s", key)
	}
	if typ == "url" && defaultValue != "" && !isSafeRuleBehaviorURL(defaultValue) {
		return fmt.Errorf("behavior url param must be a site path or HTTPS URL: %s", key)
	}
	if isRuleBehaviorRegexKey(key) {
		rawDefault, exists := spec["default"]
		if !exists || rawDefault == nil {
			return nil
		}
		source, ok := rawDefault.(string)
		if !ok {
			return fmt.Errorf("behavior.params.%s.default must be a string regex", key)
		}
		if err := validateBrowserSafeRegex("behavior.params."+key+".default", source, 300); err != nil {
			return err
		}
	}
	return nil
}

func isSafeRuleBehaviorURL(value string) bool {
	source := strings.TrimSpace(value)
	if source == "" || behaviorUnsafeURL.MatchString(source) {
		return false
	}
	if strings.HasPrefix(source, "/") {
		return !strings.HasPrefix(source, "//")
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validateRuleBehaviorConditions(raw any) (map[string]bool, error) {
	// conditions/actions 是 FBIF 声明式行为引擎的字段；FoodTalks 系 behavior（命令式）
	// 天然没有——缺失视为空，不能报错（S3 前靠「merge 基线永远是 FBIF」掩盖了这条路径）。
	if raw == nil {
		return map[string]bool{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("behavior conditions must be an array")
	}
	ids := map[string]bool{}
	for _, item := range items {
		condition, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("behavior condition must be an object")
		}
		id := strings.TrimSpace(fmt.Sprint(condition["id"]))
		if id == "" {
			return nil, fmt.Errorf("behavior condition missing id")
		}
		if ids[id] {
			return nil, fmt.Errorf("duplicate behavior condition: %s", id)
		}
		ids[id] = true
		groups := 0
		var entries []any
		for _, key := range []string{"all", "any"} {
			if rawEntries, ok := condition[key]; ok {
				groups++
				entries, ok = rawEntries.([]any)
				if !ok || len(entries) == 0 {
					return nil, fmt.Errorf("behavior condition %s has invalid %s", id, key)
				}
			}
		}
		if groups != 1 {
			return nil, fmt.Errorf("behavior condition %s must have exactly one of all or any", id)
		}
		for _, entry := range entries {
			conditionEntry, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("behavior condition %s entry must be an object", id)
			}
			if strings.TrimSpace(fmt.Sprint(conditionEntry["path"])) == "" {
				return nil, fmt.Errorf("behavior condition %s entry missing path", id)
			}
			switch fmt.Sprint(conditionEntry["op"]) {
			case "eq", "neq", "exists", "includes":
			default:
				return nil, fmt.Errorf("unknown behavior condition op: %v", conditionEntry["op"])
			}
		}
	}
	return ids, nil
}

func validateRuleBehaviorActions(raw any, conditionIDs map[string]bool, params map[string]any) error {
	if raw == nil { // 同 conditions：命令式 behavior 无 actions，缺失视为空。
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("behavior actions must be an array")
	}
	for _, item := range items {
		action, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("behavior action must be an object")
		}
		when := strings.TrimSpace(fmt.Sprint(action["when"]))
		if !conditionIDs[when] {
			return fmt.Errorf("behavior action references unknown condition: %s", when)
		}
		if fmt.Sprint(action["type"]) != "insert_image_before" {
			return fmt.Errorf("unknown behavior action type: %v", action["type"])
		}
		if fmt.Sprint(action["target"]) != "first_ref_heading" {
			return fmt.Errorf("unknown behavior action target: %v", action["target"])
		}
		srcParam := strings.TrimSpace(fmt.Sprint(action["src_param"]))
		if srcParam == "" {
			return fmt.Errorf("behavior insert image action missing src_param")
		}
		if _, ok := params[srcParam]; !ok {
			return fmt.Errorf("behavior action references unknown param: %s", srcParam)
		}
	}
	return nil
}

func ruleBehaviorNumber(value any) float64 {
	n, err := ruleValueAsFloat(value)
	if err != nil {
		return 0
	}
	return n
}

func validateRuleConfigKeys(config map[string]any) error {
	for key := range config {
		// image 是网站渲染器的结构化字段（{width:...}），不走扁平白名单
		if key == "modules" || key == "schema_version" || key == "image" {
			continue
		}
		if !isKnownRuleField(key) {
			return fmt.Errorf("unknown rule field: %s", key)
		}
	}
	if rawModules, ok := config["modules"]; ok {
		modules, ok := rawModules.(map[string]any)
		if !ok {
			return fmt.Errorf("modules must be an object")
		}
		for moduleName, rawModule := range modules {
			// 结构化模块单独校验（不走扁平字段白名单）：
			//   mctoc 仅 {enabled}；fbif_footer 是页脚库（segments/presets，规则数据）
			if moduleName == "mctoc" {
				continue
			}
			if moduleName == "fbif_footer" {
				if err := validateFBIFFooterModule(rawModule); err != nil {
					return err
				}
				continue
			}
			if moduleName == "ft_series_materials" {
				if err := validateFtSeriesMaterialsModule(rawModule); err != nil {
					return err
				}
				continue
			}
			if moduleName == "asset_library" {
				if err := validateAssetLibraryModule(rawModule); err != nil {
					return err
				}
				continue
			}
			if moduleName == "bottom_blocks" {
				if err := validateBottomBlocksModule(rawModule); err != nil {
					return err
				}
				continue
			}
			if moduleName == "article_footer_defaults" {
				if err := validateArticleFooterDefaultsModule(rawModule); err != nil {
					return err
				}
				continue
			}
			allowedFields, ok := ruleModuleFields[moduleName]
			if !ok {
				return fmt.Errorf("unknown rule module: %s", moduleName)
			}
			module, ok := rawModule.(map[string]any)
			if !ok {
				return fmt.Errorf("%s module must be an object", moduleName)
			}
			seenModuleFields := map[string]bool{}
			for key := range module {
				canonicalKey := canonicalRuleModuleField(moduleName, key)
				if !isKnownRuleField(canonicalKey) {
					return fmt.Errorf("unknown rule field: %s", key)
				}
				if !allowedFields[canonicalKey] {
					return fmt.Errorf("%s field is not allowed in %s module", key, moduleName)
				}
				if seenModuleFields[canonicalKey] {
					return fmt.Errorf("duplicate rule field: %s", canonicalKey)
				}
				seenModuleFields[canonicalKey] = true
			}
		}
		seenAcrossModules := map[string]bool{}
		for _, moduleName := range ruleModuleOrder {
			module, _ := modules[moduleName].(map[string]any)
			for key := range module {
				canonicalKey := canonicalRuleModuleField(moduleName, key)
				if seenAcrossModules[canonicalKey] {
					return fmt.Errorf("duplicate rule field: %s", canonicalKey)
				}
				seenAcrossModules[canonicalKey] = true
			}
		}
	}
	return nil
}

func isKnownRuleField(key string) bool {
	if _, ok := ruleNumberFields[key]; ok {
		return true
	}
	return ruleColorFields[key] || ruleBooleanFields[key] || ruleTextFields[key] || ruleStructuredFields[key]
}

func canonicalRuleModuleField(moduleName, key string) string {
	if moduleName == "more_articles" {
		switch key {
		case "enabled":
			return "more_articles_enabled"
		case "default_cards":
			return "more_articles_default_cards"
		}
	}
	return key
}

func flattenRuleConfig(config map[string]any) map[string]any {
	flat := map[string]any{}
	for k, v := range config {
		if k == "modules" || k == "schema_version" {
			continue
		}
		if !isKnownRuleField(k) {
			continue
		}
		flat[k] = v
	}
	modules, _ := config["modules"].(map[string]any)
	for _, moduleName := range ruleModuleOrder {
		rawModule := modules[moduleName]
		module, ok := rawModule.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range module {
			canonicalKey := canonicalRuleModuleField(moduleName, k)
			if !isKnownRuleField(canonicalKey) {
				continue
			}
			flat[canonicalKey] = v
		}
	}
	return flat
}

func validateRuleStructuredField(key string, value any) error {
	if key == "more_articles_default_cards" {
		return validateMoreArticlesDefaultCards(value)
	}
	return nil
}

// validateFBIFFooterModule 校验页脚库（config.modules.fbif_footer）结构：
//
//	{ enabled?, active_preset_id?, segment_order?[], segments?{key:{html_lines:[]string}}, presets?[{id,name,segments{key:bool}}] }
//
// html_lines 走解析器白名单；保留受控的 inline style/<img>/<a>/<table>，拒绝可执行、
// 可嵌入、表单标签以及危险 URL/CSS。验证不重写 HTML，避免默认页脚 golden 字节漂移。
func validateFBIFFooterModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("fbif_footer module must be an object")
	}
	allowed := map[string]bool{"enabled": true, "active_preset_id": true, "segment_order": true, "segments": true, "presets": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown fbif_footer field: %s", k)
		}
	}
	if rawSegments, exists := m["segments"]; exists {
		segs, ok := rawSegments.(map[string]any)
		if !ok {
			return fmt.Errorf("fbif_footer segments must be an object")
		}
		if len(segs) > 100 {
			return fmt.Errorf("fbif_footer has too many segments")
		}
		for segKey, segVal := range segs {
			sv, ok := segVal.(map[string]any)
			if !ok {
				return fmt.Errorf("fbif_footer segment %q must be an object", segKey)
			}
			for key := range sv {
				if key != "html_lines" {
					return fmt.Errorf("fbif_footer segment %q has unknown field: %s", segKey, key)
				}
			}
			lines, ok := sv["html_lines"].([]any)
			if !ok {
				return fmt.Errorf("fbif_footer segment %q html_lines must be an array", segKey)
			}
			if len(lines) > 200 {
				return fmt.Errorf("fbif_footer segment %q has too many html_lines", segKey)
			}
			combinedLines := make([]string, 0, len(lines))
			for lineIndex, l := range lines {
				s, ok := l.(string)
				if !ok {
					return fmt.Errorf("fbif_footer segment %q html_lines must be strings", segKey)
				}
				if err := validateFooterHTML(s); err != nil {
					return fmt.Errorf("fbif_footer.segments.%s.html_lines[%d]: %w", segKey, lineIndex, err)
				}
				combinedLines = append(combinedLines, s)
			}
			// 渲染器会用换行拼接所有片段；再校验一次拼接结果，阻断把标签名、属性或
			// table 上下文拆到多行后绕过逐行解析的攻击。
			if err := validateFooterHTML(strings.Join(combinedLines, "\n")); err != nil {
				return fmt.Errorf("fbif_footer.segments.%s.html_lines: %w", segKey, err)
			}
		}
	}
	if presets, ok := m["presets"].([]any); ok {
		for _, p := range presets {
			pm, ok := p.(map[string]any)
			if !ok {
				return fmt.Errorf("fbif_footer preset must be an object")
			}
			if id, ok := pm["id"].(string); !ok || strings.TrimSpace(id) == "" {
				return fmt.Errorf("fbif_footer preset 缺少 id")
			}
		}
	}
	return nil
}

// validateFtSeriesMaterialsModule 校验「什么是」系列素材库（config.modules.ft_series_materials）：
//
//	{ sections?{n:url}, groups?{name:url}, cards?{name:url}, author_card_aliases?{alias:name}, footer_default?{author_card,group,bd_card} }
//
// 所有 URL 允许站内相对路径（/assets/...）或 HTTPS；走 isSafeRuleBehaviorURL 防注入。
func validateFtSeriesMaterialsModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("ft_series_materials module must be an object")
	}
	allowed := map[string]bool{"sections": true, "groups": true, "cards": true, "author_card_aliases": true, "footer_default": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown ft_series_materials field: %s", k)
		}
	}
	checkURLMap := func(key string) error {
		v, ok := m[key]
		if !ok {
			return nil
		}
		mm, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("ft_series_materials %s must be an object", key)
		}
		for ik, iv := range mm {
			s, ok := iv.(string)
			if !ok {
				return fmt.Errorf("ft_series_materials %s.%s must be a string URL", key, ik)
			}
			if s != "" && !isSafeRuleBehaviorURL(s) {
				return fmt.Errorf("ft_series_materials %s.%s must be a site path or HTTPS URL", key, ik)
			}
		}
		return nil
	}
	for _, key := range []string{"sections", "groups", "cards"} {
		if err := checkURLMap(key); err != nil {
			return err
		}
	}
	if v, ok := m["author_card_aliases"]; ok {
		aliases, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("ft_series_materials author_card_aliases must be an object")
		}
		for k, iv := range aliases {
			s, ok := iv.(string)
			if !ok {
				return fmt.Errorf("ft_series_materials author_card_aliases.%s must be a string", k)
			}
			if len(k) > 80 || len(s) > 80 {
				return fmt.Errorf("ft_series_materials author_card_aliases.%s too long", k)
			}
		}
	}
	if v, ok := m["footer_default"]; ok {
		fd, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("ft_series_materials footer_default must be an object")
		}
		fdAllowed := map[string]bool{"author_card": true, "group": true, "bd_card": true}
		for k := range fd {
			if !fdAllowed[k] {
				return fmt.Errorf("unknown ft_series_materials footer_default field: %s", k)
			}
		}
	}
	return nil
}

// validateArticleFooterDefaultsModule 校验文章元信息驱动的底部默认：
//
//	{
//	  schema_version:1, enabled?:bool,
//	  author_aliases:{ group_id:[]string },
//	  profiles:[{
//	    id,name, match:{author_alias_group},
//	    block_visibility:{block_id:bool},
//	    artifact_visibility?:{artifact_id:bool},
//	    subheading_size?:"16"（二级标题字号，只填数字）
//	  }]
//	}
//
// 这里只允许“作者等于别名组”这一类条件。复杂条件继续留在 behavior 的受限动作系统之外，
// 避免 footer 默认变成难审计的规则引擎。
func validateArticleFooterDefaultsModule(raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("article_footer_defaults module must be an object")
	}
	allowed := map[string]bool{"schema_version": true, "enabled": true, "author_aliases": true, "profiles": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown article_footer_defaults field: %s", k)
		}
	}
	if v, ok := m["schema_version"]; ok {
		n, err := ruleValueAsFloat(v)
		if err != nil || n != 1 {
			return fmt.Errorf("article_footer_defaults requires schema_version 1")
		}
	}
	if v, ok := m["enabled"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("article_footer_defaults enabled must be boolean")
		}
	}
	aliasIDs := map[string]bool{}
	if rawAliases, ok := m["author_aliases"]; ok {
		aliases, ok := rawAliases.(map[string]any)
		if !ok {
			return fmt.Errorf("article_footer_defaults author_aliases must be an object")
		}
		if len(aliases) > 500 {
			return fmt.Errorf("article_footer_defaults has too many author alias groups")
		}
		for groupID, rawList := range aliases {
			if !assetLibraryID.MatchString(groupID) {
				return fmt.Errorf("article_footer_defaults author alias group %q is invalid", groupID)
			}
			aliasIDs[groupID] = true
			list, ok := rawList.([]any)
			if !ok {
				return fmt.Errorf("article_footer_defaults author_aliases.%s must be an array", groupID)
			}
			if len(list) > 40 {
				return fmt.Errorf("article_footer_defaults author_aliases.%s has too many entries", groupID)
			}
			for i, item := range list {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("article_footer_defaults author_aliases.%s[%d] must be a string", groupID, i)
				}
				if strings.TrimSpace(s) == "" || len([]rune(s)) > 80 {
					return fmt.Errorf("article_footer_defaults author_aliases.%s[%d] is invalid", groupID, i)
				}
				if behaviorUnsafeTemplate.MatchString(s) {
					return fmt.Errorf("article_footer_defaults author_aliases.%s[%d] contains unsafe text", groupID, i)
				}
			}
		}
	}
	rawProfiles, ok := m["profiles"]
	if !ok {
		return nil
	}
	profiles, ok := rawProfiles.([]any)
	if !ok {
		return fmt.Errorf("article_footer_defaults profiles must be an array")
	}
	if len(profiles) > 500 {
		return fmt.Errorf("article_footer_defaults has too many profiles")
	}
	seenProfiles := map[string]bool{}
	for i, rawProfile := range profiles {
		profile, ok := rawProfile.(map[string]any)
		if !ok {
			return fmt.Errorf("article_footer_defaults profiles[%d] must be an object", i)
		}
		allowedProfile := map[string]bool{"id": true, "name": true, "match": true, "block_visibility": true, "artifact_visibility": true, "subheading_size": true}
		for k := range profile {
			if !allowedProfile[k] {
				return fmt.Errorf("article_footer_defaults profiles[%d] has unknown field: %s", i, k)
			}
		}
		id, _ := profile["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" || !assetLibraryID.MatchString(id) {
			return fmt.Errorf("article_footer_defaults profiles[%d].id is invalid", i)
		}
		if seenProfiles[id] {
			return fmt.Errorf("article_footer_defaults has duplicate profile id: %s", id)
		}
		seenProfiles[id] = true
		if rawName, ok := profile["name"]; ok {
			name, ok := rawName.(string)
			if !ok {
				return fmt.Errorf("article_footer_defaults profiles[%d].name must be string", i)
			}
			if len([]rune(name)) > 80 {
				return fmt.Errorf("article_footer_defaults profiles[%d].name too long", i)
			}
		}
		match, ok := profile["match"].(map[string]any)
		if !ok {
			return fmt.Errorf("article_footer_defaults profiles[%d].match must be an object", i)
		}
		for k := range match {
			if k != "author_alias_group" {
				return fmt.Errorf("article_footer_defaults profiles[%d].match has unknown field: %s", i, k)
			}
		}
		groupID, ok := match["author_alias_group"].(string)
		groupID = strings.TrimSpace(groupID)
		if !ok || groupID == "" || !assetLibraryID.MatchString(groupID) {
			return fmt.Errorf("article_footer_defaults profiles[%d].match.author_alias_group is invalid", i)
		}
		if !aliasIDs[groupID] {
			return fmt.Errorf("article_footer_defaults profile %q references unknown author alias group: %s", id, groupID)
		}
		visibility, ok := profile["block_visibility"].(map[string]any)
		if !ok {
			return fmt.Errorf("article_footer_defaults profiles[%d].block_visibility must be an object", i)
		}
		if len(visibility) > 100 {
			return fmt.Errorf("article_footer_defaults profiles[%d].block_visibility has too many entries", i)
		}
		for blockID, rawEnabled := range visibility {
			if !assetLibraryID.MatchString(blockID) {
				return fmt.Errorf("article_footer_defaults profiles[%d].block_visibility block id %q is invalid", i, blockID)
			}
			if _, ok := rawEnabled.(bool); !ok {
				return fmt.Errorf("article_footer_defaults profiles[%d].block_visibility.%s must be boolean", i, blockID)
			}
		}
		if rawSize, ok := profile["subheading_size"]; ok {
			n, err := ruleValueAsFloat(rawSize)
			if err != nil || n < 12 || n > 32 {
				return fmt.Errorf("article_footer_defaults profiles[%d].subheading_size must be a number between 12 and 32", i)
			}
		}
		if rawArtifactVisibility, ok := profile["artifact_visibility"]; ok {
			artifactVisibility, ok := rawArtifactVisibility.(map[string]any)
			if !ok {
				return fmt.Errorf("article_footer_defaults profiles[%d].artifact_visibility must be an object", i)
			}
			if len(artifactVisibility) > 100 {
				return fmt.Errorf("article_footer_defaults profiles[%d].artifact_visibility has too many entries", i)
			}
			for artifactID, rawEnabled := range artifactVisibility {
				if !assetLibraryID.MatchString(artifactID) {
					return fmt.Errorf("article_footer_defaults profiles[%d].artifact_visibility artifact id %q is invalid", i, artifactID)
				}
				if _, ok := rawEnabled.(bool); !ok {
					return fmt.Errorf("article_footer_defaults profiles[%d].artifact_visibility.%s must be boolean", i, artifactID)
				}
			}
		}
	}
	return nil
}

// validateRuleBehaviorLists 校验 behavior.lists（网站识别词表/标记列表，红线下放）。
//
//	{ wechat_trailer_markers?:[]string, wechat_tip_signatures?:[]string,
//	  image_caption_keywords?:[]string, title_keywords?:[]string, ... }
//
// 缺省（nil）允许。每个 list 值必须为字符串数组；做 XSS 防护与长度上限；
// 名含 marker/signature/regex/pattern 的项额外做 RE2 可编译校验
// （客户端用 new RegExp 编译，开头 (?i) 由客户端适配为 i flag）。
func validateRuleBehaviorLists(raw any) error {
	if raw == nil {
		return nil
	}
	lists, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("behavior lists must be an object")
	}
	for key, value := range lists {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("behavior list %q must be an array", key)
		}
		if len(items) > 200 {
			return fmt.Errorf("behavior list %q has too many items", key)
		}
		isRegex := isRuleBehaviorRegexKey(key)
		for index, item := range items {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("behavior list %q items must be strings", key)
			}
			if len([]rune(s)) > 300 {
				return fmt.Errorf("behavior list %q item too long", key)
			}
			if behaviorUnsafeTemplate.MatchString(s) {
				return fmt.Errorf("behavior list %q item contains unsafe HTML", key)
			}
			if isRegex {
				path := fmt.Sprintf("behavior.lists.%s[%d]", key, index)
				if err := validateBrowserSafeRegex(path, s, 300); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateMoreArticlesDefaultCards(value any) error {
	cards, ok := value.([]any)
	if !ok {
		return fmt.Errorf("more_articles_default_cards must be an array")
	}
	for i, rawCard := range cards {
		card, ok := rawCard.(map[string]any)
		if !ok {
			return fmt.Errorf("more_articles_default_cards[%d] must be an object", i)
		}
		if err := validateMoreArticleCardString(card, "href", i, isSafeWeChatArticleURL); err != nil {
			return err
		}
		if err := validateMoreArticleCardString(card, "imgurl", i, isSafeImageURL); err != nil {
			return err
		}
		if title, ok := card["title"]; ok {
			if _, ok := title.(string); !ok {
				return fmt.Errorf("more_articles_default_cards[%d].title must be a string", i)
			}
		}
		if rawImages, ok := card["image_urls"]; ok {
			images, ok := rawImages.([]any)
			if !ok {
				return fmt.Errorf("more_articles_default_cards[%d].image_urls must be an array", i)
			}
			for imageIndex, rawImage := range images {
				image, ok := rawImage.(string)
				if !ok {
					return fmt.Errorf("more_articles_default_cards[%d].image_urls[%d] must be a string", i, imageIndex)
				}
				image = strings.TrimSpace(image)
				if image != "" && !isSafeImageURL(image) {
					return fmt.Errorf("more_articles_default_cards[%d].image_urls[%d] must be a safe image URL", i, imageIndex)
				}
			}
		}
	}
	return nil
}

func validateMoreArticleCardString(card map[string]any, key string, index int, valid func(string) bool) error {
	rawValue, ok := card[key]
	if !ok {
		return nil
	}
	value, ok := rawValue.(string)
	if !ok {
		return fmt.Errorf("more_articles_default_cards[%d].%s must be a string", index, key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !valid(value) {
		return fmt.Errorf("more_articles_default_cards[%d].%s is invalid", index, key)
	}
	return nil
}

func isSafeWeChatArticleURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "mp.weixin.qq.com" && host != "weixin.qq.com" {
		return false
	}
	return u.Path == "/s" || strings.HasPrefix(u.Path, "/s/")
}

func isSafeImageURL(raw string) bool {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Hostname() != ""
	case "":
		return strings.HasPrefix(value, "//") && u.Hostname() != ""
	case "data":
		data := u.Opaque
		comma := strings.Index(data, ",")
		return strings.HasPrefix(strings.ToLower(data), "image/") && comma > len("image/") && comma < len(data)-1
	default:
		return false
	}
}

func ruleValueAsFloat(value any) (float64, error) {
	var n float64
	var err error
	switch v := value.(type) {
	case float64:
		n = v
	case float32:
		n = float64(v)
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case json.Number:
		n, err = v.Float64()
	case string:
		n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
	default:
		return 0, fmt.Errorf("not a number")
	}
	if err != nil {
		return 0, err
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("not a finite number")
	}
	return n, nil
}
