# Silo architecture — first vertical slice

Silo runs Evolution CMS; it is not a dependency of the application. A standalone
Go executable owns Docker Compose infrastructure outside the application tree.
The initial commands are `version`, `doctor`, `up [-d] [--port N] [--https-port N]`,
`ps`, and `down`.

## Evidence and scope

Reviewed before implementation:

- [Salo2](https://github.com/evolution-cms/salo2/tree/beb4277bab6de3060448b0549872622eed9c697b):
  `InstallCommand.php` generates application-local Compose and rewrites an env
  file; its PHP 8.4 Apache runtime enables rewrite, GD, mysqli, PDO and ZIP.
- [Sail](https://github.com/laravel/sail/tree/603f570a1b190b14ec9b39026894f3a0ce16359e):
  `bin/sail` provides lifecycle/command proxies, Compose detection and terminal
  handling. Its shell-based OS handling and sourcing of project `.env` are not
  suitable for a native Windows CLI.
- Evolution's `core/composer.json`, `core/bootstrap.php` and immutable dotenv
  loading establish project markers and allow container environment overrides.

The first runtime is PHP 8.4 with Apache and MariaDB 11.4. Other PHP versions,
nginx, FrankenPHP, Xdebug, command proxies, Podman, installers, self-update and
release/image publishing are deliberately later work.

## Code boundaries

```text
cmd/silo/           process entry point and exit status
internal/cli/       command parsing, doctor, lifecycle orchestration
internal/project/   read-only project detection and canonical identity
internal/state/     external state, credentials, Compose and runtime assets
internal/bootstrap/ first database initialization and streaming SQL restore
internal/docker/    direct Docker subprocess execution, no shell evaluation
```

The initial implementation uses only the Go standard library. JSON is emitted
to `compose.yaml`: JSON is valid YAML and avoids a template escaping/parser
dependency. Docker Compose is the runtime authority, not a Go Docker SDK.

## Project detection

Walk from the working directory towards the filesystem root. A project needs
`index.php`, `core/bootstrap.php`, and a valid `core/composer.json` whose `name`
is `evolution-cms/evolution` or `evolutioncms/evolution`. Detection does not require
installed vendor dependencies; doctor reports their absence separately.
Canonicalize symlinks before hashing the absolute path. Windows path identity
is case-insensitive. Equal basenames in different directories remain isolated.
Moving a project creates a new identity; old state and volumes are retained.
Windows and WSL paths intentionally have separate identities and home stores.

## External state

Default root: `~/.silo`; `SILO_HOME` may choose an absolute external directory.
Reject a state root inside the detected application, including symlink aliases.

```text
~/.silo/projects/<path-hash>/
    metadata.json     schema, canonical root, HTTP port, runtime selection
    compose.yaml      explicitly selected with -f and --project-directory
    environment       generated development DB credentials, permission 0600
    runtime/          embedded Dockerfile and PHP configuration
    tls/              optional local CA, server certificate/key and Apache config
```

A per-project exclusive lock serializes state creation and detached lifecycle
operations. Attached `up` releases it after configuration validation so `down`
can run from another terminal while logs remain attached. A complete new state
is staged in a sibling directory and renamed into place. Existing state is
reused, never silently regenerated: especially credentials and database volume
identity. Unsupported schema fails with an actionable message. Explicit `--port`
changes only the managed web binding and saved port, preserving other Compose
fields, runtime files and credentials. Complete replacement files are staged
beside their destinations. If saving metadata fails, Compose is rolled back;
after interruption, the next `up` reconciles the binding with committed metadata.
`ps` and `down` never create state. `down` retains state and database volumes;
there is no destructive volume removal command in this slice.
On Windows permissions follow the user's profile ACL; POSIX modes are not an
independent Windows access-control mechanism.

## Runtime and configuration

Compose bind-mounts the canonical application root at `/var/www/html` using the
long volume syntax and `create_host_path: false`. Runtime files are embedded in
the CLI and extracted only to state. Before public GHCR images exist, Compose
builds a local `silo-runtime:8.4-apache-v1` image from this external context.
Base PHP and MariaDB images use Amazon ECR Public's Docker Official Images
mirror (`public.ecr.aws/docker/library/...`); Docker Hub is not contacted by
these references. Package installation during build also needs Debian mirrors.
Future GitHub Actions can publish `ghcr.io/evolution-cms/silo:8.4-apache` and
replace the local build without changing the application contract.

HTTP binds only to `127.0.0.1`, default port 8080; `up --port N` selects another
port at creation or on any later startup. Compose applies the changed binding by
recreating the web service without removing the database volume. MariaDB is internal to the Compose network and has a
named persistent volume plus a readiness healthcheck. The web service waits
for database readiness. Projects use separate Compose names/networks/volumes.

`up --https-port N` enables a second loopback binding (container port 443) and
persists `https_port` alongside the existing HTTP port. Old schema-1 HTTP state
remains readable; enabling TLS refreshes only its managed runtime Dockerfile,
web service and external TLS assets. The TLS image has a separate tag and adds
Apache ssl/headers modules without changing the database service or volume.
HTTP and HTTPS both serve the application. No unconditional redirect is added.
Apache adjusts local cross-protocol Location headers when an application retains
the incoming port, leaving external URLs and response content alone.

Per-project ECDSA CA/leaf material is generated by Go's standard crypto library
under `state/tls`. The leaf covers localhost and loopback IPs; CA trust is a
separate explicit user operation. Only server certificate/key and virtual-host
configuration are mounted, never the CA key. The leaf renews within 30 days of
expiry. A certificate/configuration fingerprint in Compose labels triggers web
recreation after renewal or configuration updates. Default HTTP operation needs
no certificate. HTTPS tests verify the chain with the generated CA rather than
disabling certificate verification or installing OS trust.

Container environment supplies local DB_HOST/DB_PORT/DB_DATABASE/DB_USERNAME/
DB_PASSWORD and APP_ENV. It does not read or copy production secrets. The optional
`core/custom/.env.docker.example` may supply a simple DB_DATABASE name; credentials
are always generated. Project env files are never shell-sourced or rewritten.
Existing hardcoded PHP database configuration cannot be overridden generically;
such projects need an explicit application-specific follow-up. Before web startup,
Silo starts the DB alone and checks its bootstrap marker and table count. An empty,
unmarked DB receives the newest regular nonempty `.sql`/`.sql.gz` backup from
`assets/backup` (mtime, then filename). Input is streamed as the project DB user
with client binary mode enabled; SQL and credentials are never placed in argv.
No SQL rewriting or cross-database restore is attempted. A pending marker in the
DB volume blocks retry after interruption; a completed marker prevents future
imports even if a newer backup appears. A populated DB or an empty DB without a
backup is marked complete without importing. Markers also support safe adoption
of an empty volume created before this feature. SQL-client error output is not
echoed because it may include private records. Migrations/installers/Composer
scripts are not run automatically. A restored DB alone does not establish full
application compatibility.

The bind mount is writable for normal CMS operation, so application cache/log/
upload writes can occur. The guarantee is that Silo creates no infrastructure or
configuration in the application. Integration verification must compare Git
status before/after and separately report application prerequisites.

## Process and platform behavior

Support targets: Windows amd64, Linux amd64/arm64 and macOS amd64/arm64. Docker
must expose a local Linux engine and Compose 2.20 or newer. Native PowerShell uses
native absolute paths; WSL uses Linux paths and its own Docker integration.
Doctor checks platform, Docker client, daemon, Compose, WSL kernel where relevant,
project markers and installed PHP dependencies. Checks are bounded by timeouts.
Commands execute argument arrays and inherit standard streams and Docker context.
Compose-specific host variables are removed, and an explicit env file and project
directory prevent accidental consumption of the application's `.env`/Compose file.
Docker failures retain their exit status. Interactive application command/TTY
proxying is deferred; attached `up` delegates normal console behavior to Compose.

## Acceptance

Unit tests cover detection, path identity, invalid projects, state containment,
credential reuse, concurrent creation, Compose isolation and command failures.
Run `go test ./...`, `go vet ./...`, and cross-build all five targets. With Docker
available, validate generated Compose, start/inspect/stop an isolated fixture,
verify PHP extensions, DB connectivity and persistence without changing fixture
files. Real Evolution application integration is a separate, user-directed step;
this development stage must not start or modify another project.
