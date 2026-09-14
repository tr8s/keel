# Rollouts and rollbacks of approved updates

Once an update is approved, Keel follows its rollout, reports it on the approval message and lets approvers roll
it back with one click. This page describes what Keel does and what it needs.

## Rollout status

After Keel applies an approved update (a single approval or an approval group), it follows the rollout of every
resource it updated:

- A Deployment is live when the controller observed the update and every replica is updated, ready and available,
  with no old replica left. StatefulSets and DaemonSets are followed the same way; cron jobs have nothing to roll
  out.
- A rollout fails when the Deployment reports `ProgressDeadlineExceeded` (see `progressDeadlineSeconds`), or when it
  is not live within `APPROVAL_ROLLOUT_TIMEOUT` (default `10m`, set it with `extraEnv` in the chart).
- The failure reason comes from the pods of the resource, ie: `ImagePullBackOff` or `CrashLoopBackOff`. The chart
  RBAC already lets Keel read pods; events are not used.

The Slack approval message (compact layout, `SLACK_APPROVAL_COMPACT=true`) is updated in place while the rollout
progresses, and once it is live or failed. When a rollout fails, Keel also replies in the thread of the approval
message, mentioning the approvers.

Resources without approvals are not followed.

## Roll back

The live and failed states, and the failure reply, offer a **Roll back** button. `@keel rollback <approval
identifier>` does the same. The same rule as approving applies: the request must come from the approvals channel.
Keel records who rolled back on the approval and in the audit log.

A rollback sets every resource updated from the approval back to the image it ran before, pinned by digest
(`registry/repository@sha256:...`). Pinning matters with a moving tag and `imagePullPolicy: Always`: restarting pods
on the tag would pull the rolled back image again. **Database changes, such as migrations, are not rolled back.**

Nothing changes when a resource was updated again after the approval; the approval message then says why. A
rollback is possible while the approval exists, that is until its deadline (`keel.sh/approvalDeadline`, default 24
hours).

### What Keel keeps on the resource

A rollback adds two annotations to each rolled back resource:

- `keel.sh/trackedImages`: the image reference each pinned container keeps tracking, ie: `{"api":"registry/app:main"}`.
  Polling and update checks keep following that tag although the container image is pinned by digest.
- `keel.sh/rollbackHold`: the rolled back digest and revision per image repository. Keel does not offer them again,
  also not a rebuild of the same revision.

A new revision asks for approval as usual. Applying it sets the containers back to the tracked tag and removes both
annotations.

### Deploys outside Keel

A later `helm upgrade`, `kubectl apply` or deploy script sets the container image back to the tag in its manifests,
so the pods pull whatever the tag points at, which can be the rolled back image. Keel does not prevent that. The
annotations stay on the resource, so Keel still does not offer the held image, but the deploy already rolled it
out. To stay on the previous image, pin it in the chart values or publish a fix before deploying.

## Restarts

Keel keeps approvals in SQLite under `/data`. Enable `persistence` in the chart so approvals, rollouts and rollback
data survive restarts, and set the Deployment strategy to `Recreate`:

```yaml
persistence:
  enabled: true
deploymentStrategy:
  type: Recreate
```

The volume is ReadWriteOnce, so with the default RollingUpdate strategy an upgrade starts the new Keel pod while the
old one still holds the volume, and the new pod never starts. For an existing install, upgrade with
`--set deploymentStrategy.type=Recreate`; the chart also clears the rollingUpdate settings the Deployment has.

The Keel image runs as uid and gid 666, while a new volume is usually owned by root. Keel then can not create its
database, the startup probe fails and the container restarts forever. The log shows:

```text
sql store connector: can't reach DB, waiting error="unable to open database file: no such file or directory" uri=/data/keel.db
```

Keel also logs `data directory /data is not writable by uid 666: set podSecurityContext.fsGroup ...` at startup. The
chart sets `podSecurityContext.fsGroup: 666` when persistence is enabled and `fsGroup` is not set, which lets the
Keel group write to the volume. With your own manifests or an older chart, set it yourself:

```yaml
podSecurityContext:
  fsGroup: 666
```

When Keel starts, it waits for its resources to load and then resumes the rollouts that were in progress and applies
the rollbacks that were requested but not applied. A rollout that finished or timed out meanwhile is closed on its
first check, so no message keeps showing a rollout in progress.
