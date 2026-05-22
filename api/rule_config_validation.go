package main

import (
	"encoding/json"
	"fmt"
	"math"
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
	"attribution_font_size":    {min: 10, max: 20},
	"blockquote_padding_left":  {min: 0, max: 32},
	"blockquote_border_width":  {min: 0, max: 8},
	"md_heading_max_chars":     {min: 10, max: 120},
	"decorative_image_max_px":  {min: 120, max: 1000},
	"more_articles_slots":      {min: 0, max: 8},
	"banner_overlay_alpha":     {min: 0, max: 1},
	"banner_title_x":           {min: 0, max: 400},
	"banner_title_y":           {min: 0, max: 220},
	"banner_title_width":       {min: 300, max: 1000},
	"banner_title_box_height":  {min: 40, max: 220},
	"banner_title_font_size":   {min: 24, max: 80},
	"banner_title_line_height": {min: 32, max: 100},
	"banner_title_max_lines":   {min: 1, max: 4},
}

var ruleColorFields = map[string]bool{
	"text_color":  true,
	"link_color":  true,
	"muted_color": true,
}

var ruleBooleanFields = map[string]bool{
	"footer_enabled": true,
	"skip_upload":    true,
}

var ruleTextFields = map[string]bool{
	"font_family": true,
}

var ruleModuleOrder = []string{"theme", "supporting", "more_articles", "workflow"}

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
	},
	"supporting": {
		"caption_font_size":       true,
		"attribution_font_size":   true,
		"blockquote_padding_left": true,
		"blockquote_border_width": true,
		"md_heading_max_chars":    true,
		"decorative_image_max_px": true,
	},
	"more_articles": {
		"more_articles_slots":      true,
		"banner_overlay_alpha":     true,
		"banner_title_x":           true,
		"banner_title_y":           true,
		"banner_title_width":       true,
		"banner_title_box_height":  true,
		"banner_title_font_size":   true,
		"banner_title_line_height": true,
		"banner_title_max_lines":   true,
	},
	"workflow": {
		"footer_enabled": true,
		"skip_upload":    true,
	},
}

var ruleHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var ruleFontFamily = regexp.MustCompile(`^[A-Za-z0-9一-龥 -]{1,40}$`)

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
	return validateRuleConfig(p.Config)
}

func validateRuleConfigKeys(config map[string]any) error {
	for key := range config {
		if key == "modules" || key == "schema_version" {
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
				if !isKnownRuleField(key) {
					return fmt.Errorf("unknown rule field: %s", key)
				}
				if !allowedFields[key] {
					return fmt.Errorf("%s field is not allowed in %s module", key, moduleName)
				}
				if seenModuleFields[key] {
					return fmt.Errorf("duplicate rule field: %s", key)
				}
				seenModuleFields[key] = true
			}
		}
		seenAcrossModules := map[string]bool{}
		for _, moduleName := range ruleModuleOrder {
			module, _ := modules[moduleName].(map[string]any)
			for key := range module {
				if seenAcrossModules[key] {
					return fmt.Errorf("duplicate rule field: %s", key)
				}
				seenAcrossModules[key] = true
			}
		}
	}
	return nil
}

func isKnownRuleField(key string) bool {
	if _, ok := ruleNumberFields[key]; ok {
		return true
	}
	return ruleColorFields[key] || ruleBooleanFields[key] || ruleTextFields[key]
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
			if !isKnownRuleField(k) {
				continue
			}
			flat[k] = v
		}
	}
	return flat
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
