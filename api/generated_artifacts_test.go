package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGeneratedArtifacts(t *testing.T, publicDir string) {
	t.Helper()
	registryPath := filepath.Join(publicDir, "js", "rule-packs", "registry.js")
	blockPath := filepath.Join(publicDir, "js", "blocks", "block-registry.gen.js")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(blockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, []byte(renderRegistryJS()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockPath, []byte(renderBlockRegistryJS()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedArtifactsStatusCoversPresenceAndContent(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedArtifacts(t, dir)
	status := currentGeneratedArtifactsStatus(dir)
	if !status.ready() {
		t.Fatalf("matching generated artifacts should be ready: %+v", status)
	}

	if err := os.Remove(filepath.Join(dir, "js", "rule-packs", "registry.js")); err != nil {
		t.Fatal(err)
	}
	status = currentGeneratedArtifactsStatus(dir)
	if status.RegistryPresent || status.RegistryHashMatches || !status.BlockRegistryPresent || !status.BlockRegistryHashMatches {
		t.Fatalf("missing registry status is wrong: %+v", status)
	}

	writeGeneratedArtifacts(t, dir)
	blockPath := filepath.Join(dir, "js", "blocks", "block-registry.gen.js")
	if err := os.WriteFile(blockPath, []byte("// drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	status = currentGeneratedArtifactsStatus(dir)
	if !status.BlockRegistryPresent || status.BlockRegistryHashMatches || status.ready() {
		t.Fatalf("drifted block registry status is wrong: %+v", status)
	}
}

func TestGeneratedArtifactsStartupPolicy(t *testing.T) {
	missing := t.TempDir()
	if err := validateGeneratedArtifactsForStartup(missing, false); err != nil {
		t.Fatalf("development may warn for source-only checkout: %v", err)
	}
	if err := validateGeneratedArtifactsForStartup(missing, true); err == nil || !strings.Contains(err.Error(), "无法读取") {
		t.Fatalf("production must reject missing generated artifacts, got: %v", err)
	}

	drift := t.TempDir()
	writeGeneratedArtifacts(t, drift)
	registryPath := filepath.Join(drift, "js", "rule-packs", "registry.js")
	if err := os.WriteFile(registryPath, []byte("// drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateGeneratedArtifactsForStartup(drift, false); err == nil {
		t.Fatal("existing-but-drifted generated artifact must fail in development too")
	}
}

func TestProductionRuntimeUsesExplicitEnvironment(t *testing.T) {
	t.Setenv(runtimeEnvironmentEnv, "production")
	if !productionRuntime() {
		t.Fatal("ENV=production must enable production startup policy")
	}
	t.Setenv(runtimeEnvironmentEnv, "development")
	if productionRuntime() {
		t.Fatal("development must not be treated as production")
	}
}
