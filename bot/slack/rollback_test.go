package slack

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

// deployedGroupApproval - the trackeid group approval with the rollouts of both members, which can be rolled back
func deployedGroupApproval(apiState, portalState types.RolloutState) *types.Approval {
	started := time.Now().Add(-time.Hour)
	target := func(state types.RolloutState, name string) types.RolloutTarget {
		t := types.RolloutTarget{
			State:      state,
			Marker:     "update " + name,
			Ready:      2,
			Desired:    2,
			StartedAt:  started,
			FinishedAt: started.Add(41 * time.Second),
			Containers: []types.RolloutContainer{{Name: "app", Image: "registry.example.com/tr8s/" + name + ":main", PreviousDigest: "sha256:96df4af0000000000000000000000000000000000000000000000000000000", NewDigest: "sha256:e3a91130000000000000000000000000000000000000000000000000000000"}},
		}
		if state == types.RolloutStateFailed {
			t.Ready, t.Reason, t.FinishedAt = 1, "ImagePullBackOff", started.Add(10*time.Minute)
		}
		if state == types.RolloutStateRolling {
			t.Ready, t.FinishedAt = 1, time.Time{}
		}
		return t
	}
	req := approvedGroupApproval(target(apiState, "trackeid-api"), target(portalState, "trackeid-portal"))
	req.ID = "0b5e4a2c-approval"
	return req
}

const (
	menuAccessory   = `"accessory":{"action_id":"approval_menu","options":[`
	menuOptions     = `"options":[{"text":{"text":"View changes","type":"plain_text"},"url":"https://github.com/tr8s/trackeid/compare/5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b...8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910","value":"view_changes"},{"text":{"text":"Roll back…","type":"plain_text"},"value":"rollback_request 0b5e4a2c-approval"}],"type":"overflow"`
	requestAction   = `"action_id":"rollback_request"`
	requestValue    = `"value":"0b5e4a2c-approval"`
	fallbackButton  = `"text":{"emoji":true,"text":"Roll back…","type":"plain_text"}`
	failureButton   = `"text":{"emoji":true,"text":"Roll back","type":"plain_text"}`
	confirmQuestion = "Roll back api, portal to 5f55a51? Database changes are not rolled back."
)

func TestCompactRollback(t *testing.T) {
	tests := []struct {
		name     string
		approval func() *types.Approval
		contains []string
		excludes []string
	}{
		{
			name:     "live offers view changes and roll back in a menu without a confirm",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive) },
			contains: []string{
				`"text":":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>","type":"mrkdwn"}`,
				menuAccessory,
				menuOptions,
			},
			excludes: []string{`"confirm"`, requestAction, `"type":"actions"`},
		},
		{
			name:     "failed offers view changes and roll back in a menu without a confirm",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed) },
			contains: []string{":x: portal not ready after 10m · ImagePullBackOff", menuAccessory, menuOptions},
			excludes: []string{`"confirm"`},
		},
		{
			name: "view changes falls back to the commit without the running revision",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.CurrentRevision = ""
				return req
			},
			contains: []string{`"url":"https://github.com/tr8s/trackeid/commit/8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910","value":"view_changes"`, "rollback_request 0b5e4a2c-approval"},
			excludes: []string{`"confirm"`},
		},
		{
			name: "roll back button without a link to the changes",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.SourceURL = "https://git.example.com/tr8s/trackeid"
				return req
			},
			contains: []string{
				`"elements":[{"text":":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>","type":"mrkdwn"}],"type":"context"`,
				requestAction,
				fallbackButton,
				requestValue,
			},
			excludes: []string{`"type":"overflow"`, "View changes", `"confirm"`},
		},
		{
			name: "nothing to roll back while rolling out",
			approval: func() *types.Approval {
				return deployedGroupApproval(types.RolloutStateRolling, types.RolloutStateLive)
			},
			contains: []string{":hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2"},
			excludes: []string{"rollback_request"},
		},
		{
			name: "nothing to roll back once rolled back, also after the rollback went live",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				now := time.Now()
				req.RolledBackBy, req.RolledBackAt = "U02ROLLBACK", &now
				return req
			},
			contains: []string{
				":rewind: Rolled back to 5f55a51 by <@U02ROLLBACK> · database changes are not rolled back",
				":white_check_mark: Live on api, portal in 41s",
			},
			excludes: []string{"rollback_request", "approved by"},
		},
		{
			name: "refused rollback",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.RollbackFailure = "trackeid-api was updated again since"
				return req
			},
			contains: []string{":warning: Roll back failed: trackeid-api was updated again since"},
			excludes: []string{"rollback_request"},
		},
		{
			name: "nothing to roll back without the previous images",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.Rollout[1].Containers[0].PreviousDigest = ""
				return req
			},
			contains: []string{":white_check_mark: Live on api, portal"},
			excludes: []string{"rollback_request"},
		},
		{
			name: "nothing to roll back on a superseded approval",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
				return req
			},
			contains: []string{":fast_forward: Superseded by 9c1d2e3"},
			excludes: []string{"rollback_request"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, _ := createCompactBlockMessage(tt.approval())
			rendered := renderBlocks(t, blocks)
			for _, expected := range tt.contains {
				if !strings.Contains(rendered, expected) {
					t.Errorf("expected %q in: %s", expected, rendered)
				}
			}
			for _, unexpected := range tt.excludes {
				if strings.Contains(rendered, unexpected) {
					t.Errorf("did not expect %q in: %s", unexpected, rendered)
				}
			}
		})
	}
}

func TestRolloutFailureMessageOffersRollbackButton(t *testing.T) {
	blocks, _ := createRolloutFailureMessage(deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed))
	rendered := renderBlocks(t, blocks)
	for _, expected := range []string{"<@U01ABCDEF> :x: portal not ready after 10m · ImagePullBackOff", requestAction, failureButton, requestValue} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected %q in: %s", expected, rendered)
		}
	}
	for _, unexpected := range []string{`"type":"overflow"`, `"confirm"`} {
		if strings.Contains(rendered, unexpected) {
			t.Errorf("did not expect %q in the failure reply: %s", unexpected, rendered)
		}
	}
}

func TestCreateRollbackConfirmation(t *testing.T) {
	req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	blocks, text := createRollbackConfirmation(req)
	rendered := renderBlocks(t, blocks)

	if text != confirmQuestion {
		t.Errorf("confirmation text = %q, want %q", text, confirmQuestion)
	}
	for _, expected := range []string{
		`"text":"` + confirmQuestion + `","type":"mrkdwn"`,
		`"action_id":"rollback_confirm","style":"danger","text":{"emoji":true,"text":"Roll back","type":"plain_text"},"type":"button","value":"0b5e4a2c-approval ` + req.RollbackFingerprint() + `"`,
		`"action_id":"rollback_cancel","text":{"emoji":true,"text":"Cancel","type":"plain_text"},"type":"button","value":"0b5e4a2c-approval"`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected %q in: %s", expected, rendered)
		}
	}
}

func TestRollbackConfirmationTextFitsLimit(t *testing.T) {
	req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	req.IncludesMigration = true
	req.Rollout = nil
	for i := 1; i <= 15; i++ {
		req.SetRolloutTarget(types.RolloutTarget{
			Identifier: fmt.Sprintf("deployment/trackeid/trackeid-worker-with-a-rather-long-name-%02d", i),
			Name:       fmt.Sprintf("trackeid-worker-with-a-rather-long-name-%02d", i),
			State:      types.RolloutStateLive,
			Containers: []types.RolloutContainer{{Name: "app", Image: "registry.example.com/tr8s/worker:main", PreviousDigest: "sha256:96df4af0"}},
		})
	}

	_, text := createRollbackConfirmation(req)
	if length := utf8.RuneCountInString(text); length > maxConfirmTextLength {
		t.Errorf("confirmation has %d characters, more than %d: %q", length, maxConfirmTextLength, text)
	}
	for _, expected := range []string{"Roll back worker-with-a-rather-long-name-01, ", " more to 5f55a51?", "This change included a database migration"} {
		if !strings.Contains(text, expected) {
			t.Errorf("expected %q in %q", expected, text)
		}
	}
}

type fakeApprovals struct {
	approvals map[string]*types.Approval
}

func (f *fakeApprovals) SetApprovalMessage(id, channel, timestamp string) error {
	return nil
}

func (f *fakeApprovals) GetByID(id string) (*types.Approval, error) {
	approval, ok := f.approvals[id]
	if !ok {
		return nil, errors.New("record not found")
	}
	return approval, nil
}

type postedEphemeral struct {
	interaction interactionContext
	user        string
	blocks      slack.Blocks
	text        string
}

type fakeResponder struct {
	posted   []postedEphemeral
	replaced []string
	deleted  int
}

func (r *fakeResponder) PostEphemeral(interaction interactionContext, user string, blocks slack.Blocks, text string) error {
	r.posted = append(r.posted, postedEphemeral{interaction: interaction, user: user, blocks: blocks, text: text})
	return nil
}

func (r *fakeResponder) ReplaceEphemeral(interaction interactionContext, text string) error {
	r.replaced = append(r.replaced, text)
	return nil
}

func (r *fakeResponder) DeleteEphemeral(interaction interactionContext) error {
	r.deleted++
	return nil
}

type rollbackFlow struct {
	bot       *Bot
	responder *fakeResponder
	responses chan *bot.ApprovalResponse
}

func newRollbackFlow(approval *types.Approval) *rollbackFlow {
	responses := make(chan *bot.ApprovalResponse, 1)
	responder := &fakeResponder{}
	return &rollbackFlow{
		bot: &Bot{
			approvalsRespCh:  responses,
			approvalMessages: &fakeApprovals{approvals: map[string]*types.Approval{approval.ID: approval}},
			interactions:     responder,
		},
		responder: responder,
		responses: responses,
	}
}

func (f *rollbackFlow) response() *bot.ApprovalResponse {
	select {
	case resp := <-f.responses:
		return resp
	default:
		return nil
	}
}

var channelInteraction = interactionContext{channelID: "C0APPROVALS", responseURL: "https://hooks.slack.com/actions/T0/1/abc"}

func TestRollbackAsksTheUserToConfirm(t *testing.T) {
	tests := []struct {
		name        string
		action      *slack.BlockAction
		interaction interactionContext
	}{
		{
			name:        "roll back in the menu",
			action:      &slack.BlockAction{ActionID: approvalMenuActionID, SelectedOption: slack.OptionBlockObject{Value: "rollback_request 0b5e4a2c-approval"}},
			interaction: channelInteraction,
		},
		{
			name:        "roll back button without a link to the changes",
			action:      &slack.BlockAction{ActionID: rollbackRequestActionID, Value: "0b5e4a2c-approval"},
			interaction: channelInteraction,
		},
		{
			name:        "roll back button of the failure reply in the thread",
			action:      &slack.BlockAction{ActionID: rollbackRequestActionID, Value: "0b5e4a2c-approval"},
			interaction: interactionContext{channelID: "C0APPROVALS", threadTS: "1726320000.000100", responseURL: "https://hooks.slack.com/actions/T0/2/def"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed)
			f := newRollbackFlow(approval)
			f.bot.handleAction("U02ROLLBACK", tt.action, tt.interaction)

			if resp := f.response(); resp != nil {
				t.Fatalf("expected no rollback before the confirmation, got %+v", resp)
			}
			if approval.RolledBackBy != "" || len(f.responder.replaced) != 0 || f.responder.deleted != 0 {
				t.Fatalf("expected nothing but the confirmation, got %+v", f.responder)
			}
			if len(f.responder.posted) != 1 {
				t.Fatalf("expected one confirmation, got %d", len(f.responder.posted))
			}
			posted := f.responder.posted[0]
			if posted.user != "U02ROLLBACK" || posted.interaction != tt.interaction || posted.text != confirmQuestion {
				t.Errorf("expected the confirmation for U02ROLLBACK where they acted, got %+v", posted)
			}
			if rendered := renderBlocks(t, posted.blocks); !strings.Contains(rendered, `"action_id":"rollback_confirm"`) || !strings.Contains(rendered, `"action_id":"rollback_cancel"`) {
				t.Errorf("expected Roll back and Cancel buttons, got: %s", rendered)
			}
		})
	}
}

func TestConfirmedRollbackIsRequested(t *testing.T) {
	approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	f := newRollbackFlow(approval)
	confirm := &slack.BlockAction{ActionID: rollbackConfirmActionID, Value: approval.ID + " " + approval.RollbackFingerprint()}

	f.bot.handleAction("U02ROLLBACK", confirm, channelInteraction)

	resp := f.response()
	if resp == nil || !resp.Rollback || resp.User != "U02ROLLBACK" || resp.Text != "rollback 0b5e4a2c-approval "+approval.RollbackFingerprint() {
		t.Fatalf("expected the confirmed rollback by U02ROLLBACK to be requested, got %+v", resp)
	}
	if len(f.responder.replaced) != 1 || f.responder.replaced[0] != ":rewind: Rolling back api, portal to 5f55a51…" || len(f.responder.posted) != 0 {
		t.Errorf("expected the confirmation to be replaced, got %+v", f.responder)
	}
}

func TestCancelledRollbackOnlyRemovesTheConfirmation(t *testing.T) {
	approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	f := newRollbackFlow(approval)

	f.bot.handleAction("U02ROLLBACK", &slack.BlockAction{ActionID: rollbackCancelActionID, Value: approval.ID}, channelInteraction)

	if resp := f.response(); resp != nil {
		t.Fatalf("expected no rollback, got %+v", resp)
	}
	if f.responder.deleted != 1 || len(f.responder.replaced) != 0 || len(f.responder.posted) != 0 || approval.RolledBackBy != "" {
		t.Errorf("expected only the confirmation to be removed, got %+v", f.responder)
	}
}

func TestStaleRollbackConfirmationIsRefused(t *testing.T) {
	tests := []struct {
		name   string
		change func(approval *types.Approval) string
		reason string
	}{
		{
			name: "already rolled back",
			change: func(approval *types.Approval) string {
				fingerprint := approval.RollbackFingerprint()
				now := time.Now()
				approval.RolledBackBy, approval.RolledBackAt = "U03OTHER", &now
				return fingerprint
			},
			reason: ":no_entry_sign: Not rolled back: the approved update was already rolled back",
		},
		{
			name: "rolled out again since",
			change: func(approval *types.Approval) string {
				fingerprint := approval.RollbackFingerprint()
				approval.Rollout[0].Marker = "a newer update"
				return fingerprint
			},
			reason: ":no_entry_sign: Not rolled back: the rollout changed since the rollback was confirmed",
		},
		{
			name: "superseded",
			change: func(approval *types.Approval) string {
				fingerprint := approval.RollbackFingerprint()
				approval.Rollout = nil
				approval.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
				return fingerprint
			},
			reason: ":no_entry_sign: Not rolled back: nothing was deployed with this approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
			f := newRollbackFlow(approval)
			fingerprint := tt.change(approval)

			f.bot.handleAction("U02ROLLBACK", &slack.BlockAction{ActionID: rollbackConfirmActionID, Value: approval.ID + " " + fingerprint}, channelInteraction)

			if resp := f.response(); resp != nil {
				t.Fatalf("expected the stale confirmation to be refused, got %+v", resp)
			}
			if len(f.responder.replaced) != 1 || f.responder.replaced[0] != tt.reason {
				t.Errorf("expected the confirmation to be replaced with %q, got %+v", tt.reason, f.responder.replaced)
			}
		})
	}

	// asking again for an approval that can not be rolled back explains why instead of confirming
	approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	now := time.Now()
	approval.RolledBackBy, approval.RolledBackAt = "U03OTHER", &now
	f := newRollbackFlow(approval)
	f.bot.handleAction("U02ROLLBACK", &slack.BlockAction{ActionID: rollbackRequestActionID, Value: approval.ID}, channelInteraction)
	if len(f.responder.posted) != 1 || f.responder.posted[0].text != ":no_entry_sign: Not rolled back: the approved update was already rolled back" {
		t.Errorf("expected an explanation instead of a confirmation, got %+v", f.responder.posted)
	}
}

func TestViewChangesIsIgnored(t *testing.T) {
	approval := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	f := newRollbackFlow(approval)
	view := &slack.BlockAction{ActionID: approvalMenuActionID, SelectedOption: slack.OptionBlockObject{
		Value: viewChangesValue,
		URL:   "https://github.com/tr8s/trackeid/compare/5f55a51...8f714c1",
	}}

	f.bot.handleAction("U02ROLLBACK", view, channelInteraction)

	if resp := f.response(); resp != nil {
		t.Fatalf("expected View changes to be ignored, got %+v", resp)
	}
	if len(f.responder.posted) != 0 || len(f.responder.replaced) != 0 || f.responder.deleted != 0 {
		t.Errorf("expected no message for View changes, got %+v", f.responder)
	}

	// the approve button stays an approval
	f.bot.handleAction("U01ABCDEF", &slack.BlockAction{ActionID: bot.ApprovalResponseKeyword, Value: "group/trackeid/trackeid:e3a9113"}, channelInteraction)
	if resp := f.response(); resp == nil || resp.Rollback || resp.Status != types.ApprovalStatusApproved {
		t.Errorf("expected the approve button to stay an approval, got %+v", resp)
	}
}
