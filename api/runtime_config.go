package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	runtimeConfigSchemaEnv     = "FBIF_CONFIG_SCHEMA_VERSION"
	runtimeConfigSchemaVersion = "1"
)

type runtimeConfigSchemaStatus struct {
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Matches  bool   `json:"matches"`
}

func currentRuntimeConfigSchemaStatus() runtimeConfigSchemaStatus {
	actual := strings.TrimSpace(os.Getenv(runtimeConfigSchemaEnv))
	return runtimeConfigSchemaStatus{
		Expected: runtimeConfigSchemaVersion,
		Actual:   actual,
		Matches:  actual == runtimeConfigSchemaVersion,
	}
}

func validateRuntimeConfigSchema() error {
	status := currentRuntimeConfigSchemaStatus()
	if !status.Matches {
		return fmt.Errorf("%s=%q, expected %s", runtimeConfigSchemaEnv, status.Actual, status.Expected)
	}
	return nil
}
