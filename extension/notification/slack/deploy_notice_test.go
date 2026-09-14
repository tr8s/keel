package slack

import (
	"testing"

	"github.com/keel-hq/keel/types"
)

func TestSlackSendSkipsUpdatesReportedByDeployNotices(t *testing.T) {
	client := &recordingSlackClient{}
	s := &sender{slackClient: client, channels: []string{"deploys"}}

	for _, level := range []types.Level{types.LevelDebug, types.LevelInfo, types.LevelSuccess} {
		err := s.Send(types.EventNotification{
			Message:  "Successfully updated deployment trackeid/trackeid-api",
			Type:     types.NotificationDeploymentUpdate,
			Level:    level,
			Metadata: map[string]string{types.DeployNoticeMetadataKey: "true"},
		})
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	if len(client.channels) != 0 {
		t.Fatalf("expected the update reported by a deploy notice not to be sent, got %v", client.channels)
	}

	// failures and updates without a deploy notice are still sent
	if err := s.Send(types.EventNotification{Message: "update failed", Type: types.NotificationDeploymentUpdate, Level: types.LevelError}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := s.Send(types.EventNotification{Message: "Successfully updated", Type: types.NotificationDeploymentUpdate, Level: types.LevelSuccess, Metadata: map[string]string{"name": "trackeid-api"}}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(client.channels) != 2 {
		t.Errorf("expected the other notifications to be sent, got %v", client.channels)
	}
}
