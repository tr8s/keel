# Deploy notices

Keel can post a Slack message for every update it applies to a resource that needs no approval, and keep that message
up to date while the update rolls out. A deploy notice uses the compact approval layout (workload, commit, commit list,
migration warning, author and a link to the changes) without buttons or menus, and ends with the rollout status. Updates
of resources that need approval keep their approval message; an update never gets both.

## Enable

| Setting | Chart value | Default | Meaning |
| --- | --- | --- | --- |
| `SLACK_DEPLOY_NOTICES` | `slack.deployNotices` | `false` | Post deploy notices |
| `SLACK_BOT_TOKEN` | `slack.botToken` | | Bot token (`xoxb-`), required |
| `SLACK_CHANNELS` | `slack.channel` | `general` | Channel of notices without `keel.sh/notify` (the first one of the list) |
| `SLACK_DEPLOY_NOTICES_MENTION` | `slack.deployNoticesMention` | none | Mention in the thread reply about a failed rollout, ie: `<!here>` or `<!subteam^S0123ABCD>` |
| `SLACK_APPROVAL_MIGRATION_NOTE` | `slack.approvalMigrationNote` | `Keel does not run migrations.` | Shown after "Includes a database migration." |

```yaml
slack:
  enabled: true
  botToken: xoxb-...
  channel: deploys
  deployNotices: true
  deployNoticesMention: "<!here>"
```

Deploy notices only use `chat.postMessage` and `chat.update` with the bot token:

- The Slack app needs the bot token scope `chat:write`. No app token, Socket Mode or event subscription is needed.
- Invite the bot to every channel it posts notices to (`/invite @keel`), including the channels named by
  `keel.sh/notify`. Otherwise Slack refuses the post and Keel logs a warning.
- Without `slack.appToken` the chart leaves `SLACK_APP_TOKEN` out, Keel logs at info level that the approvals bot is not
  started, and runs normally. With an app token the approvals bot starts as before and notices work alongside it.

Deploy notices cover the updates of the Kubernetes provider (Deployments, StatefulSets, DaemonSets and cron jobs).

## Channel

A notice goes to the first channel of `keel.sh/notify` on the resource (`deploys` and `#deploys` both work), otherwise
to the first channel of `SLACK_CHANNELS`. Each notice is one message in one channel.

## Grouping

Resources with the same `keel.sh/approvalGroup` in the same namespace that update to the same revision (the
`org.opencontainers.image.revision` label of the new image, or the tag when the image has none) share one notice. A
member that updates later joins the notice and the message is updated in place. Without a group, every resource gets
its own notice per revision (or digest).

When a newer revision of the same group or resource is deployed while the older one still rolls out, the older notice
ends with `:fast_forward: Superseded by <short sha>`.

## Rollout status

The last line of a notice follows the rollout the same way as for approved updates (see
[approval-rollouts.md](approval-rollouts.md)):

```
:hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2
:white_check_mark: Live on api, portal in 30s
:x: portal not ready after 10m · ImagePullBackOff
```

The message is updated when the state or the ready counts change, at most once every 3 seconds. When a resource fails
to roll out, Keel also replies in the thread of the notice, after `SLACK_DEPLOY_NOTICES_MENTION` when it is set.

## Notifications

For an update that has a notice, the Slack notification sender skips the "Successfully updated ..." notification,
whatever `NOTIFICATION_LEVEL` is. Error notifications are still sent, and other notification senders (webhooks,
Teams, mail, ...) are unchanged; their success notification carries the metadata `deployNotice: "true"`.

## Restarts

A notice is stored in the Keel database with the approvals, as an archived record of kind `notice`: its channel and
message timestamp, its members, and the rollout of every member. After a restart Keel resumes the rollouts in progress
and updates the existing messages in place, closing out rollouts that finished or timed out meanwhile. A notice that was
recorded but not posted before the restart is posted then. Notices are kept for 24 hours.
