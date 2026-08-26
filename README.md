# DORA 5 metrics rollout action

This public Docker action records explicit, versioned rollout facts in GitHub's Deployments API. It does not infer that a successful job changed production, caused an impairment, restored a service or performed rework. Those claims require independent evidence in the collector.

All executable code is Go. The canonical wire contract is [`event.Payload`](event/payload.go), and its strict decoder and authoritative semantic validator live in [`event`](event). The portable [JSON Schema](event/schema/deployment-payload-v1.schema.json) covers structural constraints; Go validation additionally enforces cross-field rules, including case-insensitive change-SHA uniqueness and equality between a `direct_commit` change SHA and `commit_sha`.

## Security prerequisite

`event: started` creates a real GitHub Deployment with `task=dora5-rollout`. Audit every workflow, webhook and integration that consumes `deployment` or `deployment_status` events. Consumers must ignore this task unless they intentionally process DORA telemetry.

Pin this action to a complete commit SHA. Grant only:

```yaml
permissions:
  contents: read
  deployments: write
```

The action derives repository, commit, workflow path, run, attempt, branch and invocation mode from trusted `GITHUB_*` runtime variables. It defaults the job identity to `GITHUB_JOB`.

## Example

```yaml
- name: Record rollout request
  id: dora-start
  uses: emmett08/dora5-metrics-action@FULL_COMMIT_SHA
  env:
    GITHUB_TOKEN: ${{ github.token }}
  with:
    event: started
    environment: service-production
    release-unit-ref: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    rollout-group-key: service-${{ github.run_id }}
    service-id: service
    target-id: production
    target-set-id: service-production-v1
    production-traffic: "true"
    final-stage: "true"
    change-shas: ${{ github.sha }}
    change-relation-source: direct_commit
    work-type: planned
    work-reason: scheduled release

# This service-owned Go command must deploy the immutable release, verify the
# serving production release independently, and emit release-changed=true or
# release-changed=false through GITHUB_OUTPUT.
- name: Deploy and verify immutable production release
  id: verify-production
  run: >-
    go run ./cmd/deploy-and-verify
    --release-unit-ref sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

- name: Record verified production exposure
  if: ${{ steps.verify-production.outputs.release-changed == 'true' }}
  uses: emmett08/dora5-metrics-action@FULL_COMMIT_SHA
  env:
    GITHUB_TOKEN: ${{ github.token }}
  with:
    event: exposed
    deployment-id: ${{ steps.dora-start.outputs.deployment-id }}
    environment: service-production
    release-changed: "true"

- name: Record terminal result
  if: ${{ always() && steps.dora-start.outputs.deployment-id != '' }}
  uses: emmett08/dora5-metrics-action@FULL_COMMIT_SHA
  env:
    GITHUB_TOKEN: ${{ github.token }}
  with:
    event: completed
    deployment-id: ${{ steps.dora-start.outputs.deployment-id }}
    environment: service-production
    release-changed: ${{ steps.verify-production.outputs.release-changed || 'unknown' }}
    result: ${{ job.status }}
```

The completion step maps the current job status directly to the action's `success`, `failure` or `cancelled` result. It records `unknown` when the independent verifier did not produce a change fact; never derive `release-changed` from the deployment command's exit status alone. Replace the illustrative `./cmd/deploy-and-verify` with the service's Go deployment verifier while preserving its `release-changed` output contract.

Emit `exposed` only for a target declared with `production-traffic: "true"`, after independently verifying that the immutable release serves real production traffic and changed the serving release. A no-op emits only `completed` with `release-changed: "false"`. A non-production target may complete with `release-changed: "true"` without an exposure event.

Use `change-relation-source: direct_commit` only when `change-shas` contains exactly the deployed `GITHUB_SHA`. Use `release_manifest` when an immutable release manifest establishes the relation to one or more different source commits.

If the workflow job has a custom or matrix-expanded display name, pass that exact REST workflow-job name through `job-name` on every `started`, `exposed` and `completed` invocation. Otherwise the action uses the `jobs.<job_id>` value from `GITHUB_JOB`.

## Mutation behaviour

The action serializes its writes and applies a one-second mutation interval, including across separate `exposed` and `completed` invocations. It does not retry a deployment or deployment-status mutation within an invocation. Before writing, it reconciles the stable rollout-stage identity or status fact with existing GitHub records, so rerunning a continuing rollout—including a later `GITHUB_RUN_ATTEMPT`—reuses a completed mutation rather than duplicating it. If historical retries left more than one matching deployment, the action deterministically prefers a deployment with a valid rollout-start fact and then the lowest deployment ID. A timeout can leave the first invocation's result ambiguous, but a rerun resolves that ambiguity before attempting another mutation.

The stable identity distinguishes service, target, target set and environment, so matrix stages in one rollout remain separate. A non-success terminal result closes that rollout stage. Start a genuinely new rollout with a new `rollout-group-key`; the action will not reopen or overwrite the terminal result.

`event-id` has the opaque form `v1:sha256:<digest>`. The digest covers length-prefixed repository, run ID, job name, rollout key, service, target, target set and environment values; it deliberately excludes `GITHUB_RUN_ATTEMPT` so a retry can reconcile the same logical stage. Consumers should not parse the identifier.

## Development

```text
go test -race ./...
go vet ./...
go build -trimpath ./cmd/dora-action
docker build .
```

See [RELEASING.md](RELEASING.md) for immutable release guidance and [SECURITY.md](SECURITY.md) for reporting security issues.
