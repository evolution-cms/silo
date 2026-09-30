# Silo

Standalone local development environments for **Evolution CMS**, powered by Docker.

```sh
cd /path/to/your/evolution-project
silo doctor
silo up -d
```

Silo runs Evolution CMS. It is a separate Go executable: your application does
not need a Silo Composer dependency, Dockerfile, Compose file or generated web
server configuration. Silo manages its infrastructure outside your application,
under `~/.silo`.

**Status:** initial development version (`0.1.0-dev`). Build from source for now;
there is no published binary release, installer or self-update command yet.
The current runtime is **PHP 8.4 + Apache + MariaDB 11.4**. This starts an
environment for an existing project; it does not install the CMS or restore its
database. Full compatibility with an individual application must be checked
separately.

## Contents

- [Requirements](#requirements)
- [Build Silo](#build-silo)
- [First startup](#first-startup)
- [Commands](#commands)
- [Runtime and database](#runtime-and-database)
- [Configuration and state](#configuration-and-state)
- [Troubleshooting](#troubleshooting)
- [Development and tests](#development-and-tests)
- [Current scope](#current-scope)

## Requirements

| Component | Requirement |
| --- | --- |
| Windows | amd64, Docker Desktop using Linux containers |
| WSL | WSL2 with Docker integration enabled for the distribution |
| Linux | amd64 or arm64, local Docker Engine |
| macOS | amd64 or arm64, local Linux container engine such as Docker Desktop |
| Docker Compose | Compose plugin 2.20 or newer, available as `docker compose` |
| Go | 1.23 or newer, **only to build Silo** |
| Application | Evolution CMS project with the detection markers below |

Docker must be installed, running and accessible to your current user. Check:

```sh
docker --version
docker info
docker compose version
```

Use a **local Docker context**. Remote daemons are outside this first version's
scope because project bind mounts refer to the daemon's filesystem.

The first startup downloads base images and builds the PHP runtime; internet
access and free Docker disk space are required. Images come from
`public.ecr.aws/docker/library/php:8.4-apache` and
`public.ecr.aws/docker/library/mariadb:11.4`; the build also uses Debian package
mirrors. These image references do not use Docker Hub. Public Silo images on
GHCR are planned, but are not required or assumed to exist in this version.

Silo does not require PHP or Composer on the host to run. Your application's PHP
dependencies must already be installed before the CMS itself can work.

## Build Silo

Clone the source and build a native executable. No global installation or
administrator access is needed for the Silo binary.

### Windows PowerShell

```powershell
git clone https://github.com/evolution-cms/silo.git
cd silo
go build -o .\bin\silo.exe .\cmd\silo
.\bin\silo.exe version
```

Add the `bin` directory to your user `PATH`, or use the executable's full path:

```powershell
cd H:\Projects\my-evolution-site
& 'H:\Tools\silo\bin\silo.exe' doctor
& 'H:\Tools\silo\bin\silo.exe' up -d
```

Replace the sample executable and application paths with your actual paths.

### Linux, macOS and WSL2

```sh
git clone https://github.com/evolution-cms/silo.git
cd silo
go build -o ./bin/silo ./cmd/silo
./bin/silo version
```

Add that `bin` directory to `PATH`, or invoke its absolute path from your project:

```sh
cd /path/to/my-evolution-site
/path/to/silo/bin/silo doctor
/path/to/silo/bin/silo up -d
```

In WSL, build/use the **Linux** binary and Linux paths, for example
`/mnt/h/Projects/my-evolution-site`. Native Windows and WSL use different home
directories and project identities; use one consistently for a given local
environment. Running both against the same application can cause port conflicts
and produces separate database volumes.

## First startup

1. Start Docker and select Linux containers.
2. Open a terminal in your existing Evolution application.
3. Run `silo doctor` and resolve any `[FAIL]` checks.
4. Run `silo up -d`.
5. Open **http://127.0.0.1:8080**.
6. Use `silo ps` to inspect services and `silo down` to stop them.

If port 8080 is already used, choose a different port **on the first startup**:

```sh
silo up -d --port 8087
```

Then open `http://127.0.0.1:8087`. Silo remembers the port; subsequent starts use
`silo up -d`. Different projects need different HTTP ports when running together.
Automatic port selection and changing an existing environment's port are not
implemented yet. Passing a different `--port` to existing state fails instead of
silently changing infrastructure.

The first build takes longer than later startups. Detached startup waits for
Compose readiness; a ready container alone does not prove that the CMS has its
dependencies, tables, content or correct application-specific configuration.

### How Silo finds your project

Silo walks upwards from the current directory and selects the closest directory
containing all of:

```text
index.php
core/bootstrap.php
core/composer.json
```

The Composer file must identify `evolution-cms/evolution` or
`evolutioncms/evolution` in its `name` field. Commands also work from subdirectories
such as `core/custom`. An unrelated PHP application is not accepted merely
because it has an `index.php`.

Older layouts without these markers are not supported by this first slice.

## Commands

| Command | Behavior |
| --- | --- |
| `silo --help` | Show supported commands and options |
| `silo version` | Print CLI version and OS/architecture; Docker is not needed |
| `silo doctor` | Diagnose platform, Docker, Compose, Linux daemon, WSL where relevant, project and state location |
| `silo up` | Create/reuse external state and start with attached Compose logs |
| `silo up -d` | Start in the background and wait for Compose readiness |
| `silo up --detach` | Same as `silo up -d` |
| `silo up -d --port 8087` | Select HTTP port when creating an environment |
| `silo ps` | Show this project's containers |
| `silo down` | Remove this project's containers/network; keep database volume and Silo state |

`silo ps` and `silo down` report that no environment exists if the project has
never been started; they do not create one. Commands fail with nonzero exit
status on error. Docker command failures retain Docker's exit code.

For attached `silo up`, keep the terminal open for logs. Use `silo down` in a
second terminal to stop/remove the environment explicitly. Console interruption
behavior is delegated to Docker Compose.

There is deliberately no generic Compose passthrough in this version.
Unsupported commands and flags, including `silo down -v`, are rejected.

## Runtime and database

### Web service

- PHP 8.4 and Apache with `mod_rewrite`.
- PHP extensions: GD, mysqli, PDO MySQL, ZIP, mbstring and intl, in addition to
  the base PHP image's extensions.
- Application bind-mounted at `/var/www/html`.
- HTTP exposed on `127.0.0.1` only.
- Upload/post limit: 100 MB; memory limit: 256 MB; timezone: UTC.
- Runtime build context extracted from the CLI into external state.

Silo itself creates no files in the application. The application mount is
writable: normal CMS cache, log and upload operations may write to the project.
It is not a read-only preview or a filesystem sandbox.

### Database service

MariaDB 11.4 runs as `db` inside an isolated Compose network. Port 3306 is **not
published to the host**. Its data lives in a project-specific named Docker volume.
The web service waits for the database healthcheck.

The web container receives:

| Variable | Value |
| --- | --- |
| `APP_ENV` | `local` |
| `DB_CONNECTION` | `mysql` |
| `DB_HOST` | `db` |
| `DB_PORT` | `3306` |
| `DB_DATABASE` | `evo`, or the literal name from the example file below |
| `DB_USERNAME` | `silo` |
| `DB_PASSWORD` | Generated once and retained in external state |

Silo generates separate user and root passwords. The web service does not
receive the database root password.

`silo down` **does not delete database data**. Restarting with `silo up -d` reuses
the same database and credentials. Preserve the external `environment` file
alongside the database volume; manually deleting state can leave an existing
volume whose credentials no longer match newly-generated ones.

An existing site's content is not imported. Silo does not run database migrations,
the Evolution installer, Composer scripts or arbitrary project commands.

Evolution configurations that use immutable dotenv/environment values can use
these container settings without rewriting `.env`. Hardcoded PHP DB connection
values or application-specific overrides require separate integration work;
Silo cannot promise to override them. Review an application's configuration
before using it as a real integration target.

## Configuration and state

### Optional Evolution example file

If `core/custom/.env.docker.example` exists, Silo reads only a literal
`DB_DATABASE` value from it:

```dotenv
DB_DATABASE=my_local_site
```

Simple single/double quotes are allowed. Names may contain letters, numbers,
underscores and hyphens, up to 64 characters, and must begin with a letter,
number or underscore. Shell commands, variable expansion and inline comments
on this setting are not supported.

Other values are ignored. Silo never copies credentials from the example or from
the application's real `.env`, and never sources an env file as a shell script.
The selected database name is fixed when state is first created.

### State directory

```text
~/.silo/
└── projects/
    └── <project-id>/
        ├── metadata.json
        ├── compose.yaml
        ├── environment
        └── runtime/
            ├── Dockerfile
            └── php.ini
```

On native Windows, `~` is your user profile, normally `C:\Users\<you>`.
`compose.yaml` is generated as JSON, which is valid YAML accepted by Compose.
The `environment` file contains development credentials; do not commit or share
it. POSIX permissions are restricted to the owner. On Windows, protect the
directory with your user-profile ACLs.

To use a different **absolute directory outside the application**:

```powershell
# PowerShell, current terminal session
$env:SILO_HOME = 'D:\SiloState'
silo up -d
```

```sh
# Linux/macOS/WSL, current terminal session
export SILO_HOME="$HOME/.local/share/silo"
silo up -d
```

Use the same `SILO_HOME` for later `ps`, `down` and `up` commands. Changing it does
not move existing state or volumes. Silo rejects a state path inside the
application, including paths that resolve there through symlinks.

Project identity is derived from its canonical absolute path. Projects with the
same basename remain separate; symlink aliases share an identity. Moving a
project creates a new identity and leaves its previous state/data intact.
There is no state migration or reset command yet.

Silo passes its Compose file, env file, project name and project directory
explicitly. Ambient `COMPOSE_*`, `DB_*`, `MARIADB_*` and `APP_ENV` variables are
excluded from Compose subprocesses so unrelated shell settings cannot redirect
the project or replace its generated database credentials. Standard Docker
context and authentication settings are preserved.

State creation is staged and protected by a project lock. Existing damaged or
incompatible state causes an error instead of silently replacing credentials.

## Troubleshooting

| Symptom | Check/action |
| --- | --- |
| `silo` is not recognized | Use the executable's full path or add its directory to `PATH` |
| Docker CLI unavailable | Install Docker and confirm `docker --version` works in the same terminal |
| Docker daemon unavailable | Start Docker Desktop/the Docker service; check `docker info` |
| Linux containers required | Switch Docker Desktop from Windows containers to Linux containers |
| Compose unavailable | Install/enable the Compose plugin; confirm `docker compose version` |
| WSL2 not detected | Use WSL2 and enable Docker Desktop integration for that distribution |
| No Evolution project found | Change to the application directory and verify the detection markers |
| Missing `core/vendor/autoload.php` | Install the application's PHP dependencies separately; Silo does not do this automatically |
| Port already allocated | Stop the conflicting service, or choose `--port` before first creating another environment |
| Existing environment uses another port | Reuse its saved port; automatic reconfiguration is not implemented |
| Image pull/build fails | Check access to ECR Public/Debian mirrors and available Docker disk space |
| Containers run but site fails | Check application dependencies, local DB content, config and logs; container readiness is not CMS acceptance |
| Cannot lock project state | Another lifecycle command may be running; wait for it to finish |
| Incompatible/incomplete state | Preserve state and investigate; do not delete DB credentials as a repair shortcut |

An interrupted process can leave `<project-id>.lock` next to the state directory.
Only remove that specific lock after confirming no Silo lifecycle process for
the project is still active. Do not remove the environment directory or database
volume to clear a lock.

For deeper diagnostics, use Docker directly with the exact paths printed by
Silo. For example, in PowerShell:

```powershell
$state = 'C:\Users\you\.silo\projects\PROJECT_ID'
docker compose --project-name silo-PROJECT_ID --project-directory $state --env-file "$state\environment" -f "$state\compose.yaml" logs --tail 100 web db
```

Replace `PROJECT_ID` in both places. Avoid sharing `docker compose config` output
without review: its resolved configuration can contain passwords. Silo's own
startup validation uses `config --quiet`.

## Development and tests

The CLI uses the Go standard library only; there is no `go.sum` until external
module dependencies are introduced.

```sh
go test ./...
go vet ./...
go build -o ./bin/silo ./cmd/silo
```

The opt-in Docker integration test creates its own temporary Evolution-shaped
PHP fixture. It does not read or run an existing site. It builds the runtime,
tests HTTP/rewrite, PHP extensions, MariaDB connectivity, database persistence,
and unchanged fixture files. It removes only its own containers and database
volume after the test. Downloaded images/build cache remain available.

```sh
SILO_INTEGRATION=1 go test ./internal/cli -run '^TestDockerLifecycle$' -v -timeout 30m
```

PowerShell equivalent:

```powershell
$env:SILO_INTEGRATION = '1'
try {
    go test ./internal/cli -run '^TestDockerLifecycle$' -v -timeout 30m
} finally {
    Remove-Item Env:SILO_INTEGRATION
}
```

The first image build may take several minutes. An available local port is
selected for the fixture; the real application's default port is not used.

See [architecture](docs/architecture.md) for boundaries and source research,
and [validation](docs/validation.md) for the checks actually performed.

## Current scope

Implemented: project discovery, external state, PHP 8.4/Apache, MariaDB 11.4,
diagnostics, lifecycle commands and isolated test coverage.

Planned separately: additional runtimes, nginx/FrankenPHP/Xdebug, service
selection, shell/PHP/Composer/Node/database command proxies, Podman, GHCR
publishing, release binaries, installers, self-update, state reconfiguration
and optional project configuration. `.silo.yml` is not read by this version.

Silo takes runtime lessons from [Salo2](https://github.com/evolution-cms/salo2)
and CLI inspiration from [Laravel Sail](https://github.com/laravel/sail), while
keeping infrastructure outside the application.

## License

[MIT](LICENSE).
