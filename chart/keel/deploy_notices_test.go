package keel_test

import (
	"encoding/base64"
	"strings"
	"testing"
)

const slackDeployNoticesEnv = "- name: SLACK_DEPLOY_NOTICES\n              value: \"true\""

const slackDeployNoticesMentionEnv = "- name: SLACK_DEPLOY_NOTICES_MENTION\n              value: \"<!here>\""

func TestSlackDeployNoticesOffByDefault(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{"enabled": true, "botToken": "xoxb-test", "appToken": "xapp-test"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	if strings.Contains(deployment, "SLACK_DEPLOY_NOTICES") {
		t.Error("deploy notice variables rendered while deployNotices is not set")
	}
	if secret := rendered["keel/templates/secret.yaml"]; !strings.Contains(secret, "SLACK_APP_TOKEN: "+base64.StdEncoding.EncodeToString([]byte("xapp-test"))) {
		t.Errorf("secret does not render the app token when it is set:\n%s", secret)
	}
}

// Deploy notices only need the bot token: the chart renders the token, the channel and the switch, and leaves the app
// token out, so Keel does not read an empty SLACK_APP_TOKEN and does not start the Socket Mode bot.
func TestSlackDeployNoticesWithBotTokenOnly(t *testing.T) {
	rendered, err := renderChart(t, map[string]interface{}{
		"slack": map[string]interface{}{
			"enabled":              true,
			"botToken":             "xoxb-test",
			"channel":              "deploys",
			"deployNotices":        true,
			"deployNoticesMention": "<!here>",
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	deployment := rendered["keel/templates/deployment.yaml"]
	for _, want := range []string{
		slackDeployNoticesEnv,
		slackDeployNoticesMentionEnv,
		"- name: SLACK_CHANNELS\n              value: \"deploys\"",
		"secretRef:",
	} {
		if !strings.Contains(deployment, want) {
			t.Errorf("deployment does not render %q:\n%s", want, deployment)
		}
	}

	secret := rendered["keel/templates/secret.yaml"]
	if !strings.Contains(secret, "SLACK_BOT_TOKEN: "+base64.StdEncoding.EncodeToString([]byte("xoxb-test"))) {
		t.Errorf("secret does not render the bot token:\n%s", secret)
	}
	if strings.Contains(secret, "SLACK_APP_TOKEN") {
		t.Errorf("secret renders SLACK_APP_TOKEN without an app token:\n%s", secret)
	}
}
