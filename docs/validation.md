# Validation of the first Silo slice

Local validation date: 2026-09-30.

## Environment

- Host: Windows amd64, native PowerShell.
- Go: 1.27.1, downloaded to a temporary toolchain directory and SHA-256 checked
  against the Go download manifest; no global Go installation or PATH change.
- Docker Engine: 29.8.0, Linux containers through Docker Desktop.
- Docker Compose: 5.5.1.

## Passed checks

- `go test ./...`: project detection, direct state-path containment, external state,
  stable credentials, corrupted-state refusal, lifecycle locks, attached-up lock
  release, version checks, command validation and failure propagation.
- `go vet ./...`.
- Cross-compilation: Windows amd64, Linux amd64/arm64, macOS amd64/arm64.
- Native binary smoke checks: `silo version` and `silo --help`.
- `git diff --check`.
- Opt-in `TestDockerLifecycle` with real Docker:
  - generated configuration accepted by Compose;
  - PHP runtime built using the ECR Public base image;
  - HTTP request through Apache rewrite returned valid PHP 8.4 output;
  - GD, mysqli, PDO MySQL, ZIP, mbstring and intl were loaded;
  - PHP connected to MariaDB using generated local credentials;
  - an independently modified test row survived `down` and subsequent `up -d`;
  - the mounted fixture path contained both spaces and a dollar sign;
  - all fixture file names and SHA-256 hashes remained unchanged;
  - cleanup removed the fixture's containers, network and database volume.

The Docker test constructs an Evolution-shaped PHP fixture in a temporary
directory. It is deliberately not an installed Evolution CMS application.
It uses an available ephemeral localhost port and external temporary Silo state.
No real site is started, migrated or imported by these checks.

## Limits of this evidence

- Native runtime execution is verified on Windows amd64 only. Other targets
  were cross-compiled, not exercised on their own hosts.
- WSL2/macOS/Linux runtime and terminal interaction still need platform QA.
- The two symlink-specific test cases were skipped: this Windows session lacks
  the privilege to create symlinks. Their implementation is present but the alias
  behavior remains to be executed on a suitable host.
- No production or development site's database/configuration was changed.
- Actual Evolution CMS startup and application behavior are a separate,
  user-directed integration stage.
- No GitHub release, GHCR image publication, installer, commit or push was made.
- Docker image/build cache remains locally for subsequent testing. The CLI
  build at `bin/silo.exe` is ignored by Git.

See the [README](../README.md#development-and-tests) for reproducible commands.
