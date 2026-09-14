package keel_test

import (
	"strings"
	"testing"
)

func TestDeploymentStrategyUnsetByDefault(t *testing.T) {
	rendered, err := renderChart(t, nil)
	if err != nil {
		t.Fatalf("render defaults: %v", err)
	}

	if strings.Contains(rendered["keel/templates/deployment.yaml"], "strategy:") {
		t.Error("strategy rendered while deploymentStrategy is not set")
	}
}

func TestDeploymentStrategyRecreateWithPersistence(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"persistence":        map[string]interface{}{"enabled": true},
		"deploymentStrategy": map[string]interface{}{"type": "Recreate"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if !strings.Contains(deployment, "  replicas: 1\n  strategy:\n    type: Recreate\n    rollingUpdate: null\n  selector:") {
		t.Errorf("deployment does not render the Recreate strategy:\n%s", deployment)
	}
}

func TestDeploymentStrategyRollingUpdate(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"deploymentStrategy": map[string]interface{}{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]interface{}{"maxUnavailable": 1},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if !strings.Contains(deployment, "  strategy:\n    rollingUpdate:\n      maxUnavailable: 1\n    type: RollingUpdate\n") || strings.Contains(deployment, "rollingUpdate: null") {
		t.Errorf("deployment does not render the RollingUpdate strategy as given:\n%s", deployment)
	}
}
