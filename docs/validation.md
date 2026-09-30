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

## Port changes and first database restore

The extended suite checks explicit port changes on an existing environment,
including saved-port reuse, preservation of unrelated Compose fields, credential
stability, recovery after an interrupted state update and rollback on a metadata
write failure. Backup unit tests cover newest-file selection, gzip reading,
missing backups, existing tables, completion markers and blocked retries.

The Docker lifecycle test now runs two independent projects with the same
directory basename under one `SILO_HOME`. It verifies initial restore from plain
SQL and gzip, changes one running project's port, checks that the old binding is
released, and confirms that both projects retain separate data. Stopping the first
project leaves the second available; restarting it reuses the new port without
reimporting the backup. Both application fixture snapshots remain unchanged.

`TestDockerImportFailure` uses a deliberately broken test dump after one table
and row have been created. Its acceptance conditions are retained partial data,
a pending marker, no web startup and no automatic retry on the next `up`.

The real site's backup is not imported by this development/test workflow; its
first run remains a user-controlled step. Unit tests, vet, all five cross-builds
and the local Windows executable were refreshed for these changes.

## Simultaneous HTTP and HTTPS

`TestDockerHTTPS` passed against Docker Desktop with certificate verification
enabled in the test client. Its trusted roots were scoped to that client; no CA
was installed in Windows or a browser. The test checks:

- migration of a running HTTP-only environment to HTTP plus HTTPS;
- HTTP returns an ordinary application response without a Silo-forced redirect;
- trusted TLS connections work for both `127.0.0.1` and `localhost`;
- PHP reports the actual protocol;
- application redirects map HTTP-to-HTTPS and HTTPS-to-HTTP ports while retaining
  path/query components;
- either port can change without disabling the other protocol;
- both saved ports work after `down` and a subsequent `up` without flags;
- the application fixture remains unchanged.

State tests verify certificate chains/SANs, stable CA and credentials, TLS port
collision rejection, stable repeated generation and exclusion of the CA private
key from Compose mounts. Trust-store installation and a real browser's trust
behavior remain explicit user-side steps, not claims of this integration test.
