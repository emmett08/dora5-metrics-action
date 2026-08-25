# DORA 5 metrics rollout action

This public Docker action records explicit, versioned rollout facts in GitHub's Deployments API. It does not infer that a successful job changed production, caused an impairment, restored a service or performed rework. Those claims require independent evidence in the collector.

All executable code is Go. The canonical wire contract is [`event.Payload`](event/payload.go), its strict decoder and validator live in [`event`](event), and the matching JSON Schema is [`event/schema/deployment-payload-v1.schema.json`](event/schema/deployment-payload-v1.schema.json).

## Security prerequisite

`event: started` creates a real GitHub Deployment with `task=dora5-rollout`. Audit every workflow, webhook and integration that consumes `deployment` or `deployment_status` events. Consumers must ignore this task unless they intentionally process DORA telemetry.

Pin this action to a complete commit SHA. Grant only:

```yaml
permissions:
  contents: read
  deployments: write
```

The action derives repository, commit, workflow path, run, attempt, job, branch and invocation mode from trusted `GITHUB_*` runtime variables; callers cannot override them.

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
    work-reason: approved release

- name: Record verified production exposure
  uses: emmett08/dora5-metrics-action@FULL_COMMIT_SHA
  env:
    GITHUB_TOKEN: ${{ github.token }}
  with:
    event: exposed
    deployment-id: ${{ steps.dora-start.outputs.deployment-id }}
    environment: service-production
    release-changed: "true"

- name: Record terminal result
  if: always()
  uses: emmett08/dora5-metrics-action@FULL_COMMIT_SHA
  env:
    GITHUB_TOKEN: ${{ github.token }}
  with:
    event: completed
    deployment-id: ${{ steps.dora-start.outputs.deployment-id }}
    environment: service-production
    release-changed: "true"
    result: success
```

Emit `exposed` only after independently verifying that the immutable release serves real production traffic and changed the serving release. A no-op emits only `completed` with `release-changed: "false"`.

## Mutation behaviour

The action serializes its writes and applies a one-second mutation interval, including across separate `exposed` and `completed` invocations. It never retries a deployment or deployment-status mutation automatically. A timeout can leave a mutation's result ambiguous; first reconcile the stable `event-id` and GitHub deployment records before retrying a job.

## Development

```text
go test -race ./...
go vet ./...
go build -trimpath ./cmd/dora-action
docker build .
```

See [RELEASING.md](RELEASING.md) for immutable release guidance and [SECURITY.md](SECURITY.md) for reporting security issues.
