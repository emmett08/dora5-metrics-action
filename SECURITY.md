# Security policy

Report suspected credential exposure or event spoofing privately through GitHub's security-advisory interface for this repository.

- Pin this action and every deployment action to complete commit SHAs.
- Give the job only `contents: read` and `deployments: write` unless the deployment itself requires more.
- Pass `GITHUB_TOKEN` through the action step's environment; never place a token in an input, payload, output or log.
- Protect deployment workflow branches and review every change to their trusted workflow files.
- Require every unrelated `deployment` consumer to ignore `task=dora5-rollout`.
- Do not retry ambiguous GitHub mutation failures automatically.
- If the job declares a GitHub environment, use `environment.deployment: false` to avoid a second automatic deployment record.
