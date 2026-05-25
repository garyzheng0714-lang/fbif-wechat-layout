package main

import "net/http"

type rulePresetSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func writeRulePresetCatalog(w http.ResponseWriter, status int, doc rulePresetDocument) {
	presets := make([]rulePresetSummary, 0, len(doc.Presets))
	for _, preset := range doc.Presets {
		presets = append(presets, rulePresetSummary{
			ID:          preset.ID,
			Name:        preset.Name,
			Description: preset.Description,
			UpdatedAt:   preset.UpdatedAt,
		})
	}
	writeJSON(w, status, struct {
		Presets   []rulePresetSummary  `json:"presets"`
		Accounts  []ruleAccountBinding `json:"accounts"`
		UpdatedAt string               `json:"updated_at"`
	}{
		Presets:   presets,
		Accounts:  doc.Accounts,
		UpdatedAt: doc.UpdatedAt,
	})
}
