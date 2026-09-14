package slack

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/pkg/config"
	"github.com/keel-hq/keel/types"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/slack-go/slack"
)

const (
	noticeCurrentRevision = "96df4af0c85d9e0f1a2b3c4d5e6f708192a3b4c5"
	noticeRevision        = "8b4d1960a1b2c3d4e5f60718293a4b5c6d7e8f90"
	noticeParentRevision  = "5e0c2d1b0c3d4e5f60718293a4b5c6d7e8f90a1b"
	noticeNewerRevision   = "4c80a47c85d9e0f1a2b3c4d5e6f708192a3b4c5d"
)

var noticeStarted = time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)

// trackeidNotice - the deploy notice of trackeid (api, portal) 8b4d196, a change of two commits
func trackeidNotice() *types.Approval {
	return &types.Approval{
		Provider:        types.ProviderTypeKubernetes,
		Kind:            types.ApprovalKindNotice,
		Identifier:      types.NoticeIdentifier("group/trackeid/trackeid", noticeRevision),
		Group:           "trackeid/trackeid",
		CurrentVersion:  "main",
		NewVersion:      "main",
		SourceURL:       "https://github.com/tr8s/trackeid",
		CurrentRevision: noticeCurrentRevision,
		NewRevision:     noticeRevision,
		CommitSubject:   "Retry registry calls",
		CommitAuthor:    "Tim Brandin",
		ChangeCount:     2,
		ChangeCommits: types.ChangeCommits{
			{SHA: noticeRevision, Subject: "Retry registry calls"},
			{SHA: noticeParentRevision, Subject: "Label images with their commit"},
		},
		Members: types.ApprovalMembers{
			{Identifier: "deployment/trackeid/trackeid-api", Name: "trackeid-api"},
			{Identifier: "deployment/trackeid/trackeid-portal", Name: "trackeid-portal"},
		},
	}
}

// noticeTarget - the rollout of a trackeid member, finished after the duration when it is live or failed
func noticeTarget(name string, state types.RolloutState, ready int32, took time.Duration, reason string) types.RolloutTarget {
	target := types.RolloutTarget{
		Identifier: "deployment/trackeid/" + name,
		Name:       name,
		State:      state,
		Ready:      ready,
		Desired:    2,
		Reason:     reason,
		StartedAt:  noticeStarted,
	}
	if state == types.RolloutStateLive || state == types.RolloutStateFailed {
		target.FinishedAt = noticeStarted.Add(took)
	}
	return target
}

func withTargets(req *types.Approval, targets ...types.RolloutTarget) *types.Approval {
	for _, target := range targets {
		req.SetRolloutTarget(target)
	}
	return req
}

func TestCreateDeployNoticeMessage(t *testing.T) {
	const (
		commitLink = "https://github.com/tr8s/trackeid/commit/" + noticeRevision
		compare    = "https://github.com/tr8s/trackeid/compare/" + noticeCurrentRevision + "..." + noticeRevision
	)
	interactive := []string{`"type":"actions"`, `"accessory"`, `"button"`, `"overflow"`, `"action_id"`}

	tests := []struct {
		name     string
		notice   *types.Approval
		contains []string
		excludes []string
	}{
		{
			name:   "recorded",
			notice: trackeidNotice(),
			contains: []string{
				"*trackeid* → <" + commitLink + "|8b4d196>",
				"• <" + commitLink + "|8b4d196> Retry registry calls",
				"• <https://github.com/tr8s/trackeid/commit/" + noticeParentRevision + "|5e0c2d1> Label images with their commit",
				`"text":"trackeid · api, portal · Tim Brandin · <` + compare + `|View changes>"`,
				`"text":":hourglass_flowing_sand: Rolling out"`,
			},
			excludes: []string{"votes", "Approved"},
		},
		{
			name: "rolling out",
			notice: withTargets(trackeidNotice(),
				noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""),
				noticeTarget("trackeid-portal", types.RolloutStateRolling, 2, 0, ""),
			),
			contains: []string{`"text":":hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2"`},
		},
		{
			name: "live",
			notice: withTargets(trackeidNotice(),
				noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""),
				noticeTarget("trackeid-portal", types.RolloutStateLive, 2, 25*time.Second, ""),
			),
			contains: []string{`"text":":white_check_mark: Live on api, portal in 30s"`},
		},
		{
			name: "failed",
			notice: withTargets(trackeidNotice(),
				noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""),
				noticeTarget("trackeid-portal", types.RolloutStateFailed, 1, 10*time.Minute, "ImagePullBackOff"),
			),
			contains: []string{`"text":":x: portal not ready after 10m · ImagePullBackOff"`},
		},
		{
			name: "superseded",
			notice: func() *types.Approval {
				req := withTargets(trackeidNotice(), noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""))
				req.SupersededBy = noticeNewerRevision
				return req
			}(),
			contains: []string{`"text":":fast_forward: Superseded by 4c80a47"`},
			excludes: []string{"Rolling out"},
		},
		{
			name: "migration",
			notice: func() *types.Approval {
				req := trackeidNotice()
				req.IncludesMigration = true
				return req
			}(),
			contains: []string{":warning: Includes a database migration. Keel does not run migrations."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, text := createDeployNoticeMessage(tt.notice, defaultNoticeMigrationNote)
			if want := "Deploying trackeid (api, portal) 8b4d196: Retry registry calls (+1)"; text != want {
				t.Errorf("notification text = %q, want %q", text, want)
			}
			rendered := renderBlocks(t, blocks)
			for _, want := range tt.contains {
				if !strings.Contains(rendered, want) {
					t.Errorf("expected %q in:\n%s", want, rendered)
				}
			}
			for _, unwanted := range append(tt.excludes, interactive...) {
				if strings.Contains(rendered, unwanted) {
					t.Errorf("did not expect %q in:\n%s", unwanted, rendered)
				}
			}
		})
	}
}

func TestDeployNoticeFailureText(t *testing.T) {
	notice := withTargets(trackeidNotice(),
		noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""),
		noticeTarget("trackeid-portal", types.RolloutStateFailed, 1, 10*time.Minute, "ImagePullBackOff"),
	)
	if got := noticeFailureText(notice, ""); got != ":x: portal not ready after 10m · ImagePullBackOff" {
		t.Errorf("unexpected reply without mention: %q", got)
	}
	if got := noticeFailureText(notice, "<!here>"); got != "<!here> :x: portal not ready after 10m · ImagePullBackOff" {
		t.Errorf("unexpected reply with mention: %q", got)
	}
}

type noticeCall struct {
	method    string
	channel   string
	timestamp string
	values    url.Values
}

type fakeNoticeClient struct {
	calls   []noticeCall
	postErr error
}

func (c *fakeNoticeClient) PostMessage(channel string, options ...slack.MsgOption) (string, string, error) {
	_, values, _ := slack.UnsafeApplyMsgOptions("xoxb-test", channel, "https://slack.example.com/api/", options...)
	c.calls = append(c.calls, noticeCall{method: "post", channel: channel, values: values})
	if c.postErr != nil {
		return "", "", c.postErr
	}
	return "C0DEPLOYS", fmt.Sprintf("1757844000.%06d", len(c.calls)), nil
}

func (c *fakeNoticeClient) UpdateMessage(channel, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	_, values, _ := slack.UnsafeApplyMsgOptions("xoxb-test", channel, "https://slack.example.com/api/", options...)
	c.calls = append(c.calls, noticeCall{method: "update", channel: channel, timestamp: timestamp, values: values})
	return channel, timestamp, "", nil
}

func (c *fakeNoticeClient) take() []noticeCall {
	calls := c.calls
	c.calls = nil
	return calls
}

type noticeTest struct {
	t       *testing.T
	cfg     config.Config
	client  *fakeNoticeClient
	manager *approvals.DefaultManager
	poster  *noticePoster
	now     time.Time
}

func noticeConfig(channels, mention string) config.Config {
	return config.Config{
		Notifications: config.NotificationConfig{Slack: config.SlackNotificationConfig{BotToken: "xoxb-test", Channels: channels}},
		Bots:          config.BotConfig{Slack: config.SlackBotConfig{BotToken: "xoxb-test", DeployNotices: true, DeployNoticesMention: mention}},
	}
}

func newNoticeTest(t *testing.T, cfg config.Config) *noticeTest {
	t.Helper()
	store, teardown := newTestingUtils()
	t.Cleanup(teardown)
	nt := &noticeTest{
		t:       t,
		cfg:     cfg,
		client:  &fakeNoticeClient{},
		manager: approvals.New(&approvals.Opts{Store: store}),
		now:     noticeStarted.Add(time.Hour),
	}
	nt.poster = nt.newPoster()
	return nt
}

// newPoster - a poster that knows nothing about posted messages, as after a restart
func (nt *noticeTest) newPoster() *noticePoster {
	poster := newNoticePoster(nt.client, nt.manager, nt.cfg)
	poster.now = func() time.Time { return nt.now }
	return poster
}

// record records the deploy notice of the change for the trackeid member
func (nt *noticeTest) record(req *types.Approval, member string) *types.Approval {
	nt.t.Helper()
	req.Members = nil
	notice, err := nt.manager.RecordDeployNotice(req, types.ApprovalMember{Identifier: "deployment/trackeid/" + member, Name: member})
	if err != nil {
		nt.t.Fatalf("failed to record the deploy notice: %s", err)
	}
	return notice
}

func (nt *noticeTest) rollout(id string, targets ...types.RolloutTarget) {
	nt.t.Helper()
	_, err := nt.manager.UpdateRollout(id, func(approval *types.Approval) bool {
		withTargets(approval, targets...)
		return true
	})
	if err != nil {
		nt.t.Fatalf("failed to record the rollout: %s", err)
	}
}

func (nt *noticeTest) calls(method string, count int) []noticeCall {
	nt.t.Helper()
	calls := nt.client.take()
	if len(calls) != count {
		nt.t.Fatalf("expected %d calls, got %d: %+v", count, len(calls), calls)
	}
	for _, call := range calls {
		if call.method != method {
			nt.t.Fatalf("expected only %s calls, got %+v", method, calls)
		}
	}
	return calls
}

func TestDeployNoticeChannel(t *testing.T) {
	tests := []struct {
		name     string
		channels string
		notify   string
		want     string
	}{
		{name: "first configured channel", channels: " #trackeid-deploys, #other", want: "trackeid-deploys"},
		{name: "keel.sh/notify channel", channels: "trackeid-deploys", notify: "#selfled-deploys", want: "selfled-deploys"},
		{name: "no channel configured", want: "general"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nt := newNoticeTest(t, noticeConfig(tt.channels, ""))
			req := trackeidNotice()
			req.MessageChannel = tt.notify
			notice := nt.record(req, "trackeid-api")

			nt.poster.refresh(notice.ID)
			call := nt.calls("post", 1)[0]
			if call.channel != tt.want {
				t.Errorf("posted to %q, want %q", call.channel, tt.want)
			}

			stored, err := nt.manager.GetByID(notice.ID)
			if err != nil || stored.MessageChannel != "C0DEPLOYS" || stored.MessageTimestamp != "1757844000.000001" {
				t.Errorf("expected the message location to be recorded, got %+v, %v", stored, err)
			}
		})
	}
}

func TestDeployNoticePostedMessage(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", ""))
	notice := nt.record(trackeidNotice(), "trackeid-api")
	nt.record(trackeidNotice(), "trackeid-portal")

	nt.poster.refresh(notice.ID)
	call := nt.calls("post", 1)[0]
	if text := call.values.Get("text"); text != "Deploying trackeid (api, portal) 8b4d196: Retry registry calls (+1)" {
		t.Errorf("unexpected notification text %q", text)
	}
	blocks := call.values.Get("blocks")
	if !strings.Contains(blocks, "View changes") || !strings.Contains(blocks, "Rolling out") {
		t.Errorf("expected the compact notice, got %s", blocks)
	}
	for _, unwanted := range []string{`"type":"actions"`, `"accessory"`, `"action_id"`} {
		if strings.Contains(blocks, unwanted) {
			t.Errorf("did not expect %s in the notice: %s", unwanted, blocks)
		}
	}
	if call.values.Get("thread_ts") != "" {
		t.Error("did not expect the notice to be a thread reply")
	}

	// the same notice is not posted twice
	nt.poster.refresh(notice.ID)
	nt.calls("post", 0)
}

func TestDeployNoticeUpdatesAreThrottled(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", ""))
	notice := nt.record(trackeidNotice(), "trackeid-api")
	nt.poster.refresh(notice.ID)
	timestamp := nt.calls("post", 1)[0]

	// nothing changed
	nt.now = nt.now.Add(5 * time.Second)
	nt.poster.refresh(notice.ID)
	nt.calls("update", 0)

	// the rollout starts more than an interval after the post
	nt.rollout(notice.ID, noticeTarget("trackeid-api", types.RolloutStateRolling, 0, 0, ""))
	nt.poster.refresh(notice.ID)
	update := nt.calls("update", 1)[0]
	if update.channel != "C0DEPLOYS" || update.timestamp != "1757844000.000001" || !strings.Contains(update.values.Get("blocks"), "Rolling out · api 0/2") {
		t.Errorf("expected the posted message (%s) to show the rollout, got %+v", timestamp.channel, update)
	}

	// progress within the interval waits for it
	nt.now = nt.now.Add(time.Second)
	nt.rollout(notice.ID, noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""))
	nt.poster.refresh(notice.ID)
	nt.poster.flush()
	nt.calls("update", 0)

	nt.now = nt.now.Add(time.Second)
	nt.rollout(notice.ID, noticeTarget("trackeid-api", types.RolloutStateRolling, 2, 0, ""))
	nt.poster.refresh(notice.ID)
	nt.calls("update", 0)

	// once the interval passed the latest progress is shown, once
	nt.now = nt.now.Add(time.Second)
	nt.poster.flush()
	if update := nt.calls("update", 1)[0]; !strings.Contains(update.values.Get("blocks"), "Rolling out · api 2/2") {
		t.Errorf("expected the latest progress, got %s", update.values.Get("blocks"))
	}
	nt.now = nt.now.Add(10 * time.Second)
	nt.poster.flush()
	nt.calls("update", 0)
}

func TestDeployNoticeLateMemberUpdatesMessage(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", ""))
	notice := nt.record(trackeidNotice(), "trackeid-api")
	nt.poster.refresh(notice.ID)
	if text := nt.calls("post", 1)[0].values.Get("text"); text != "Deploying trackeid (api) 8b4d196: Retry registry calls (+1)" {
		t.Errorf("unexpected notification text %q", text)
	}

	joined := nt.record(trackeidNotice(), "trackeid-portal")
	if joined.ID != notice.ID {
		t.Fatalf("expected the portal to join the notice")
	}
	nt.now = nt.now.Add(noticeUpdateInterval)
	nt.poster.refresh(notice.ID)
	if text := nt.calls("update", 1)[0].values.Get("text"); text != "Deploying trackeid (api, portal) 8b4d196: Retry registry calls (+1)" {
		t.Errorf("unexpected notification text %q", text)
	}
}

func TestDeployNoticeGoesLive(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", "<!here>"))
	notice := nt.record(trackeidNotice(), "trackeid-api")
	nt.record(trackeidNotice(), "trackeid-portal")
	nt.rollout(notice.ID,
		noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""),
		noticeTarget("trackeid-portal", types.RolloutStateRolling, 2, 0, ""),
	)
	nt.poster.refresh(notice.ID)
	if blocks := nt.calls("post", 1)[0].values.Get("blocks"); !strings.Contains(blocks, "Rolling out · api 1/2 · portal 2/2") {
		t.Errorf("expected the rollout in progress, got %s", blocks)
	}

	nt.rollout(notice.ID,
		noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""),
		noticeTarget("trackeid-portal", types.RolloutStateLive, 2, 20*time.Second, ""),
	)
	nt.now = nt.now.Add(noticeUpdateInterval)
	nt.poster.refresh(notice.ID)
	if blocks := nt.calls("update", 1)[0].values.Get("blocks"); !strings.Contains(blocks, "Live on api, portal in 30s") {
		t.Errorf("expected the live rollout, got %s", blocks)
	}
}

func TestDeployNoticeFailureReply(t *testing.T) {
	for _, mention := range []string{"", "<!here>"} {
		t.Run("mention "+mention, func(t *testing.T) {
			nt := newNoticeTest(t, noticeConfig("deploys", mention))
			notice := nt.record(trackeidNotice(), "trackeid-api")
			nt.record(trackeidNotice(), "trackeid-portal")
			nt.rollout(notice.ID,
				noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""),
				noticeTarget("trackeid-portal", types.RolloutStateRolling, 1, 0, ""),
			)
			nt.poster.refresh(notice.ID)
			nt.calls("post", 1)

			nt.rollout(notice.ID,
				noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""),
				noticeTarget("trackeid-portal", types.RolloutStateFailed, 1, 10*time.Minute, "ImagePullBackOff"),
			)
			nt.now = nt.now.Add(noticeUpdateInterval)
			nt.poster.replyFailure(notice.ID)

			calls := nt.client.take()
			if len(calls) != 2 || calls[0].method != "update" || calls[1].method != "post" {
				t.Fatalf("expected the message to be updated and a thread reply, got %+v", calls)
			}
			if blocks := calls[0].values.Get("blocks"); !strings.Contains(blocks, ":x: portal not ready after 10m · ImagePullBackOff") {
				t.Errorf("expected the message to show the failure, got %s", blocks)
			}
			reply := calls[1]
			want := ":x: portal not ready after 10m · ImagePullBackOff"
			if mention != "" {
				want = mention + " " + want
			}
			if reply.channel != "C0DEPLOYS" || reply.values.Get("thread_ts") != "1757844000.000001" || reply.values.Get("text") != want {
				t.Errorf("unexpected thread reply to %s in %q: %q, want %q", reply.channel, reply.values.Get("thread_ts"), reply.values.Get("text"), want)
			}
		})
	}
}

func TestSupersededDeployNoticeGetsNoFailureReply(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", "<!here>"))
	older := nt.record(trackeidNotice(), "trackeid-api")
	nt.rollout(older.ID, noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""))
	nt.poster.refresh(older.ID)
	nt.calls("post", 1)

	newer := trackeidNotice()
	newer.Identifier = types.NoticeIdentifier("group/trackeid/trackeid", noticeNewerRevision)
	newer.NewRevision = noticeNewerRevision
	nt.record(newer, "trackeid-portal")

	nt.rollout(older.ID, noticeTarget("trackeid-api", types.RolloutStateFailed, 1, 10*time.Minute, "ImagePullBackOff"))
	nt.now = nt.now.Add(noticeUpdateInterval)
	nt.poster.replyFailure(older.ID)

	update := nt.calls("update", 1)[0]
	if blocks := update.values.Get("blocks"); !strings.Contains(blocks, ":fast_forward: Superseded by 4c80a47") {
		t.Errorf("expected the older notice to show it was superseded, got %s", blocks)
	}
}

func TestDeployNoticeResumesAfterRestart(t *testing.T) {
	nt := newNoticeTest(t, noticeConfig("deploys", ""))
	posted := nt.record(trackeidNotice(), "trackeid-api")
	nt.rollout(posted.ID, noticeTarget("trackeid-api", types.RolloutStateRolling, 1, 0, ""))
	nt.poster.refresh(posted.ID)
	nt.calls("post", 1)

	// recorded while Keel stopped before posting them
	unposted := trackeidNotice()
	unposted.Identifier = types.NoticeIdentifier("deployment/trackeid/trackeid-worker", noticeRevision)
	unposted.Group = ""
	unpostedNotice := nt.record(unposted, "trackeid-worker")
	nt.rollout(unpostedNotice.ID, noticeTarget("trackeid-worker", types.RolloutStateRolling, 0, 0, ""))

	finished := trackeidNotice()
	finished.Identifier = types.NoticeIdentifier("deployment/trackeid/trackeid-cron", noticeRevision)
	finished.Group = ""
	finishedNotice := nt.record(finished, "trackeid-cron")
	nt.rollout(finishedNotice.ID, noticeTarget("trackeid-cron", types.RolloutStateLive, 2, time.Second, ""))

	restarted := nt.newPoster()
	restarted.resume()
	post := nt.calls("post", 1)[0]
	if text := post.values.Get("text"); !strings.HasPrefix(text, "Deploying trackeid-worker 8b4d196") {
		t.Errorf("expected the unposted notice in progress to be posted, got %q", text)
	}

	// the resumed rollout finishes: the posted message is updated in place, not posted again
	nt.rollout(posted.ID, noticeTarget("trackeid-api", types.RolloutStateLive, 2, 30*time.Second, ""))
	restarted.refresh(posted.ID)
	update := nt.calls("update", 1)[0]
	if update.channel != "C0DEPLOYS" || update.timestamp != "1757844000.000001" || !strings.Contains(update.values.Get("blocks"), "Live on api in 30s") {
		t.Errorf("expected the message posted before the restart to be updated, got %+v", update)
	}
}

// Deploy notices only need the bot token: without an app token the approvals bot is not configured or started, and
// Keel starts deploy notices without logging a warning or an error.
func TestDeployNoticesStartWithoutTheBot(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()

	store, teardown := newTestingUtils()
	defer teardown()
	manager := approvals.New(&approvals.Opts{Store: store})
	cfg := noticeConfig("deploys", "")

	b := &Bot{}
	if b.Configure(cfg, nil, nil) || b.slackSocket != nil {
		t.Fatal("expected the approvals bot not to be configured without an app token")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !b.StartDeployNotices(ctx, cfg, manager) {
		t.Fatal("expected deploy notices to start with the bot token only")
	}

	bot.Run(cfg, nil, manager)

	var started, skipped bool
	for _, entry := range hook.AllEntries() {
		if entry.Level <= logrus.WarnLevel {
			t.Errorf("unexpected %s log: %s", entry.Level, entry.Message)
		}
		started = started || strings.Contains(entry.Message, "posts deploy notices")
		skipped = skipped || strings.Contains(entry.Message, "is not configured, skipping")
	}
	if !started || !skipped {
		t.Errorf("expected the bot manager to start deploy notices and skip the bot, started %v, skipped %v", started, skipped)
	}

	// without the switch nothing starts
	if (&Bot{}).StartDeployNotices(ctx, noticeConfig("deploys", ""), nil) {
		t.Error("expected deploy notices not to start without an approvals manager")
	}
	off := noticeConfig("deploys", "")
	off.Bots.Slack.DeployNotices = false
	if (&Bot{}).StartDeployNotices(ctx, off, manager) {
		t.Error("expected deploy notices to be off by default")
	}
}
