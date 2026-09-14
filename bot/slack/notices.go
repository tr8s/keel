package slack

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/pkg/config"
	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"

	log "github.com/sirupsen/logrus"
)

// noticeUpdateInterval - the least time between two updates of the message of a deploy notice
const noticeUpdateInterval = 3 * time.Second

// noticeForgetAfter - how long the poster remembers a finished notice, which keeps repeated events from updating it
const noticeForgetAfter = 10 * time.Minute

// defaultNoticeChannel - the channel of deploy notices when neither keel.sh/notify nor SLACK_CHANNELS names one, as
// for Slack notifications
const defaultNoticeChannel = "general"

// noticeClient - the Slack Web API methods of deploy notices, which only need the bot token and the chat:write scope
type noticeClient interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
	UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
}

// noticeRecords - the approvals manager methods of deploy notices
type noticeRecords interface {
	GetByID(id string) (*types.Approval, error)
	SetApprovalMessage(id, channel, timestamp string) error
	SubscribeUpdated(ctx context.Context) (<-chan *types.Approval, error)
	SubscribeRolloutFailed(ctx context.Context) (<-chan *types.Approval, error)
	ListRollouts() ([]*types.Approval, error)
}

// noticePoster posts the deploy notices the provider records, updates them in place while their rollouts progress and
// replies in their threads when a rollout fails. A single goroutine does all of it, so a notice is never posted twice.
type noticePoster struct {
	client  noticeClient
	records noticeRecords

	channel       string
	mention       string
	migrationNote string
	interval      time.Duration
	now           func() time.Time

	// by notice id, only used by the poster goroutine
	messages map[string]*noticeMessage
}

// noticeMessage - what the poster knows about the message of a notice
type noticeMessage struct {
	channel   string
	timestamp string
	// rendered is what the message shows, empty when unknown, ie: after a restart
	rendered string
	// updated is when the message was last posted or updated
	updated time.Time
	// pending is set when the notice changed while updates were throttled
	pending  bool
	finished bool
}

// StartDeployNotices - post and update the deploy notices of the updates that need no approval
// (SLACK_DEPLOY_NOTICES). It only uses the Slack Web API with the bot token, so it runs whether or not the bot is
// configured with an app token and started.
func (b *Bot) StartDeployNotices(ctx context.Context, appConfig config.Config, manager approvals.Manager) bool {
	cfg := appConfig.Bots.Slack
	if !cfg.DeployNotices {
		return false
	}
	if !cfg.DeployNoticesEnabled() {
		log.Warn("bot.slack: SLACK_DEPLOY_NOTICES needs SLACK_BOT_TOKEN with the prefix \"xoxb-\", deploy notices are off")
		return false
	}
	if manager == nil {
		return false
	}

	poster := newNoticePoster(slack.New(cfg.BotToken, slack.OptionDebug(appConfig.Debug)), manager, appConfig)
	if err := poster.start(ctx); err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("bot.slack: failed to start deploy notices")
		return false
	}
	log.WithFields(log.Fields{
		"channel": poster.channel,
	}).Info("bot.slack: deploy notices started")
	return true
}

func newNoticePoster(client noticeClient, records noticeRecords, appConfig config.Config) *noticePoster {
	cfg := appConfig.Bots.Slack
	note := strings.TrimSpace(cfg.ApprovalMigrationNote)
	if note == "" {
		note = defaultNoticeMigrationNote
	}
	return &noticePoster{
		client:        client,
		records:       records,
		channel:       noticeChannel(appConfig.Notifications.Slack.Channels),
		mention:       strings.TrimSpace(cfg.DeployNoticesMention),
		migrationNote: note,
		interval:      noticeUpdateInterval,
		now:           time.Now,
		messages:      make(map[string]*noticeMessage),
	}
}

// noticeChannel - the first channel of a comma separated list without its # prefix, the default channel when none
func noticeChannel(channels string) string {
	for _, channel := range strings.Split(channels, ",") {
		if channel = strings.TrimPrefix(strings.TrimSpace(channel), "#"); channel != "" {
			return channel
		}
	}
	return defaultNoticeChannel
}

func (p *noticePoster) start(ctx context.Context) error {
	updated, err := p.records.SubscribeUpdated(ctx)
	if err != nil {
		return err
	}
	failed, err := p.records.SubscribeRolloutFailed(ctx)
	if err != nil {
		return err
	}
	go p.run(ctx, updated, failed)
	return nil
}

func (p *noticePoster) run(ctx context.Context, updated, failed <-chan *types.Approval) {
	p.resume()

	tick := p.interval / 3
	if tick < 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case notice := <-updated:
			if notice.IsNotice() {
				p.refresh(notice.ID)
			}
		case notice := <-failed:
			if notice.IsNotice() {
				p.replyFailure(notice.ID)
			}
		case <-ticker.C:
			p.flush()
		}
	}
}

// resume posts the notices of rollouts in progress that were recorded but not posted before a restart. Posted notices
// are updated in place once the rollouts, which the provider resumes, report progress or finish.
func (p *noticePoster) resume() {
	rollouts, err := p.records.ListRollouts()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Warn("bot.slack: failed to list the deploy notices to resume")
		return
	}
	for _, notice := range rollouts {
		if notice.IsNotice() && notice.MessageTimestamp == "" && notice.RolloutState() == types.RolloutStateRolling {
			p.refresh(notice.ID)
		}
	}
}

// refresh posts the message of a deploy notice, or updates it when what it shows changed. A message is updated at most
// once per interval; a change within the interval is shown once the interval passed.
func (p *noticePoster) refresh(id string) {
	notice, err := p.records.GetByID(id)
	if err != nil {
		log.WithFields(log.Fields{
			"error":     err,
			"notice_id": id,
		}).Debug("bot.slack: deploy notice not found")
		return
	}

	blocks, text := createDeployNoticeMessage(notice, p.migrationNote)
	rendered := renderedNotice(blocks, text)
	now := p.now()

	message := p.message(notice)
	if message.timestamp == "" {
		p.post(notice, blocks, text, rendered)
		return
	}

	message.finished = noticeFinished(notice)
	switch {
	case message.rendered == rendered:
		message.pending = false
	case now.Sub(message.updated) < p.interval:
		message.pending = true
	default:
		message.updated = now
		if _, _, _, err := p.client.UpdateMessage(message.channel, message.timestamp, noticeMessageOptions(blocks, text)...); err != nil {
			log.WithFields(log.Fields{
				"error":  err,
				"notice": notice.Identifier,
			}).Warn("bot.slack: failed to update the deploy notice, trying again later")
			message.pending = true
			return
		}
		message.rendered = rendered
		message.pending = false
	}
}

// message - what the poster knows about the message of a notice, with the location recorded on the notice
func (p *noticePoster) message(notice *types.Approval) *noticeMessage {
	message := p.messages[notice.ID]
	if message == nil {
		message = &noticeMessage{}
		p.messages[notice.ID] = message
	}
	if message.timestamp == "" && notice.MessageTimestamp != "" {
		message.channel, message.timestamp = notice.MessageChannel, notice.MessageTimestamp
	}
	return message
}

// post posts the message of a notice to the channel of the notice, the default channel when it has none, and records
// where it was posted. A failed post is not tried again until the notice changes.
func (p *noticePoster) post(notice *types.Approval, blocks slack.Blocks, text, rendered string) {
	channel := strings.TrimPrefix(notice.MessageChannel, "#")
	if channel == "" {
		channel = p.channel
	}

	channelID, timestamp, err := p.client.PostMessage(channel, noticeMessageOptions(blocks, text)...)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err,
			"channel": channel,
			"notice":  notice.Identifier,
		}).Warn("bot.slack: failed to post the deploy notice, check that the bot is invited to the channel")
		return
	}

	message := p.messages[notice.ID]
	message.channel, message.timestamp = channelID, timestamp
	message.rendered, message.updated, message.pending = rendered, p.now(), false
	message.finished = noticeFinished(notice)

	if err := p.records.SetApprovalMessage(notice.ID, channelID, timestamp); err != nil {
		log.WithFields(log.Fields{
			"error":  err,
			"notice": notice.Identifier,
		}).Warn("bot.slack: failed to record where the deploy notice was posted")
	}
}

// replyFailure replies in the thread of a deploy notice when one more of its resources failed to roll out, after the
// configured mention. Superseded notices get no reply.
func (p *noticePoster) replyFailure(id string) {
	// the failure can arrive before the update that shows it
	p.refresh(id)

	notice, err := p.records.GetByID(id)
	if err != nil || notice.SupersededBy != "" || notice.RolloutState() != types.RolloutStateFailed {
		return
	}
	message := p.message(notice)
	if message.timestamp == "" {
		return
	}

	_, _, err = p.client.PostMessage(message.channel,
		slack.MsgOptionText(noticeFailureText(notice, p.mention), false),
		slack.MsgOptionTS(message.timestamp),
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error":  err,
			"notice": notice.Identifier,
		}).Warn("bot.slack: failed to reply to the deploy notice about a failed rollout")
	}
}

// flush shows the changes that were throttled once their interval passed, and forgets finished notices
func (p *noticePoster) flush() {
	now := p.now()
	for id, message := range p.messages {
		switch {
		case message.pending && now.Sub(message.updated) >= p.interval:
			p.refresh(id)
		case !message.pending && (message.finished || message.timestamp == "") && now.Sub(message.updated) > noticeForgetAfter:
			delete(p.messages, id)
		}
	}
}

func noticeMessageOptions(blocks slack.Blocks, text string) []slack.MsgOption {
	return []slack.MsgOption{
		slack.MsgOptionText(text, true),
		slack.MsgOptionBlocks(blocks.BlockSet...),
	}
}

// renderedNotice - what a notice message shows, to tell whether it changed
func renderedNotice(blocks slack.Blocks, text string) string {
	encoded, _ := json.Marshal(blocks.BlockSet)
	return text + "\n" + string(encoded)
}
