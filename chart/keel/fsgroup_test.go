package keel_test

import (
	"strings"
	"testing"
)

func TestFSGroupUnsetWithoutPersistence(t *testing.T) {
	rendered, err := renderChart(t, nil)
	if err != nil {
		t.Fatalf("render defaults: %v", err)
	}

	if strings.Contains(rendered["keel/templates/deployment.yaml"], "fsGroup") {
		t.Error("fsGroup rendered while persistence is disabled")
	}
}

func TestFSGroupDefaultsToKeelGroupWithPersistence(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"persistence": map[string]interface{}{"enabled": true},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if !strings.Contains(deployment, "      securityContext:\n        fsGroup: 666\n      containers:") {
		t.Errorf("deployment does not render fsGroup 666 with persistence:\n%s", deployment)
	}
}

func TestFSGroupKeepsOtherPodSecuritySettings(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"persistence":        map[string]interface{}{"enabled": true},
		"podSecurityContext": map[string]interface{}{"runAsNonRoot": true},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if !strings.Contains(deployment, "      securityContext:\n        fsGroup: 666\n        runAsNonRoot: true\n") {
		t.Errorf("deployment does not merge fsGroup with the pod security context:\n%s", deployment)
	}
}

func TestFSGroupSetExplicitlyIsKept(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"persistence":        map[string]interface{}{"enabled": true},
		"podSecurityContext": map[string]interface{}{"fsGroup": 1000},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if !strings.Contains(deployment, "fsGroup: 1000") || strings.Contains(deployment, "fsGroup: 666") {
		t.Errorf("deployment does not keep the explicit fsGroup:\n%s", deployment)
	}
}
