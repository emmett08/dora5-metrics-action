# Releasing

1. Run the complete verification commands in the README.
2. Review the public payload and action-input compatibility. Breaking changes require a new event version and a new major action tag.
3. Create a signed semantic-version tag from the verified commit.
4. Move only the matching major tag after verifying that it resolves to the release commit.
5. Publish release notes that identify payload, input and security changes.

Consumers should pin complete commit SHAs. A major tag is a discovery convenience, not an immutable reference.
