package keel_test

import (
	"strings"
	"testing"
)

const slackHideCommandsEnv = "- name: SLACK_APPROVAL_HIDE_COMMANDS\n              value: \"true\""

const slackCompactEnv = "- name: SLACK_APPROVAL_COMPACT\n              value: \"true\""

const slackMigrationNoteEnv = "- name: SLACK_APPROVAL_MIGRATION_NOTE\n              value: \"It runs when the new pods start; no manual step.\""

func TestSlackApprovalCommandsShownByDefault(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{"enabled": true},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if strings.Contains(deployment, "SLACK_APPROVAL_HIDE_COMMANDS") {
		t.Error("SLACK_APPROVAL_HIDE_COMMANDS rendered while hideApprovalCommands is not set")
	}
	if strings.Contains(deployment, "SLACK_APPROVAL_COMPACT") {
		t.Error("SLACK_APPROVAL_COMPACT rendered while compactApprovals is not set")
	}
	if strings.Contains(deployment, "SLACK_APPROVAL_MIGRATION_NOTE") {
		t.Error("SLACK_APPROVAL_MIGRATION_NOTE rendered while approvalMigrationNote is not set")
	}
}

func TestSlackApprovalCommandsCanBeHidden(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{"enabled": true, "hideApprovalCommands": true},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(rendered["keel/templates/deployment.yaml"], slackHideCommandsEnv) {
		t.Errorf("deployment does not render SLACK_APPROVAL_HIDE_COMMANDS=true:\n%s", rendered["keel/templates/deployment.yaml"])
	}
}

func TestSlackCompactApprovalsCanBeEnabled(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{"enabled": true, "compactApprovals": true},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(rendered["keel/templates/deployment.yaml"], slackCompactEnv) {
		t.Errorf("deployment does not render SLACK_APPROVAL_COMPACT=true:\n%s", rendered["keel/templates/deployment.yaml"])
	}
}

func TestSlackApprovalMigrationNoteCanBeSet(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{"enabled": true, "approvalMigrationNote": "It runs when the new pods start; no manual step."},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(rendered["keel/templates/deployment.yaml"], slackMigrationNoteEnv) {
		t.Errorf("deployment does not render SLACK_APPROVAL_MIGRATION_NOTE:\n%s", rendered["keel/templates/deployment.yaml"])
	}
}
