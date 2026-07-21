package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const runtimeEnvironmentEnv = "ENV"

// generatedArtifactsStatus 只暴露部署验收需要的布尔状态，不泄露服务器路径。
// “hash_matches”在这里表示 committed 字节与当前 Go 真相源生成结果完全一致。
type generatedArtifactsStatus struct {
	RegistryPresent          bool `json:"registry_present"`
	RegistryHashMatches      bool `json:"registry_hash_matches"`
	BlockRegistryPresent     bool `json:"block_registry_present"`
	BlockRegistryHashMatches bool `json:"block_registry_hash_matches"`
}

func (s generatedArtifactsStatus) ready() bool {
	return s.RegistryPresent && s.RegistryHashMatches &&
		s.BlockRegistryPresent && s.BlockRegistryHashMatches
}

func productionRuntime() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(runtimeEnvironmentEnv)), "production")
}

func inspectGeneratedArtifact(path, expected string) (present, matches bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	return true, string(raw) == expected
}

func currentGeneratedArtifactsStatus(publicDir string) generatedArtifactsStatus {
	var out generatedArtifactsStatus
	out.RegistryPresent, out.RegistryHashMatches = inspectGeneratedArtifact(
		filepath.Join(publicDir, "js", "rule-packs", "registry.js"),
		renderRegistryJS(),
	)
	out.BlockRegistryPresent, out.BlockRegistryHashMatches = inspectGeneratedArtifact(
		filepath.Join(publicDir, "js", "blocks", "block-registry.gen.js"),
		renderBlockRegistryJS(),
	)
	return out
}

// validateGeneratedArtifactsForStartup keeps local source-only workflows usable, while making
// incomplete production releases fatal. Existing-but-drifted generated files are fatal in every
// environment because they create a mixed frontend/backend schema.
func validateGeneratedArtifactsForStartup(publicDir string, production bool) error {
	checks := []struct {
		label string
		err   error
	}{
		{label: "账号注册表", err: validateRegistryConsistency(publicDir)},
		{label: "区块注册表", err: validateBlockRegistryConsistency(publicDir)},
	}
	var fatal []error
	for _, check := range checks {
		if check.err == nil {
			continue
		}
		missingOrUnreadable := strings.HasPrefix(check.err.Error(), "warn:")
		if missingOrUnreadable && !production {
			log.Printf("%s一致性校验：%v", check.label, check.err)
			continue
		}
		fatal = append(fatal, fmt.Errorf("%s一致性校验失败: %w", check.label, check.err))
	}
	return errors.Join(fatal...)
}
