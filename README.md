# iscrt

A command-line secret manager with project-organized `.env` file support.

Fork of [scrt](https://github.com/loderunner/scrt) by [@loderunner](https://github.com/loderunner) -- adds project namespacing and native `.env` import/export. All credit for the core encryption engine goes to the original project.

## Why

I needed a simple, local, free alternative to cloud secret managers like Bitwarden Secrets Manager. My requirements:

- Organize secrets by project
- Push/pull `.env` files
- Encrypted at rest (AES-256 + Argon2id)
- No cloud, no subscription, no infrastructure
- Single binary, works offline

scrt handled the encryption perfectly. I added the project and `.env` layer on top.

## Install

```bash
go install github.com/imaan/iscrt@latest
```

## Setup

```bash
# Set your master password (add to ~/.zshrc or ~/.bashrc for persistence)
export SCRT_PASSWORD="your-secure-password"
export SCRT_STORAGE=local
export SCRT_LOCAL_PATH=~/.iscrt/store.iscrt

# Initialize the encrypted store
iscrt init
```

## Usage

### Push secrets from `.env` to encrypted store

```bash
cd ~/code/my-project
iscrt env push                    # reads .env, stores under "my-project/"
iscrt env push .env.production    # specify file
iscrt env push --project custom   # custom project name
iscrt env push --mode replace     # replace all project secrets (default: merge)
```

**Merge mode** (default): adds new keys, updates changed values, leaves existing keys not in the file untouched.

**Replace mode**: sets all values from the file, then removes any keys in the project that aren't in the file.

### Pull secrets from store to `.env`

```bash
cd ~/code/my-project
iscrt env pull                    # writes .env from "my-project/" secrets
iscrt env pull --force            # overwrite existing .env
iscrt env pull .env.local         # write to specific file
```

### Run commands with injected secrets

```bash
cd ~/code/my-project
iscrt run -- npm run build                            # inject "my-project/" secrets into the child env
iscrt run --only DATABASE_URL -- npx prisma migrate deploy
iscrt run --except DEBUG_TOKEN -- ./deploy.sh         # inject everything except these keys
iscrt run --require API_KEY,DB_URL -- make release    # fail fast if keys are missing
iscrt run --no-inherit -- env                         # child gets PATH + secrets only
iscrt run --project other-app -- ./script.sh          # explicit project
```

Secrets are decrypted in memory and passed to the child process's environment only — they never touch stdout, argv, shell history, or disk. The child's exit code is propagated, and Ctrl-C / SIGTERM are forwarded to the child. This is the recommended way to use secrets in builds, deploys, and AI-agent workflows: unlike `VAR="$(iscrt get KEY)" cmd`, nothing is ever printed.

**Security notes:**

- The master password (`SCRT_PASSWORD`) is always stripped from the child's environment, so a leaky build script exposes at most the injected project secrets — never the whole store.
- The child process (and anything it spawns) *can* read the injected values. `iscrt run` prevents accidental leaks; it does not sandbox the child. When handing this to an autonomous agent, restrict which child commands it may run at the harness level (e.g. Claude Code permission rules allowing `iscrt run -- npm *` but not `iscrt run -- sh`).

### Browse secrets

```bash
iscrt env list                    # list all projects
iscrt env list my-project         # list keys in project (values masked)
iscrt env list my-project -r      # reveal full values
```

### Delete secrets

```bash
iscrt env delete my-project                 # delete all secrets in project
iscrt env delete my-project -k API_KEY      # delete single key
iscrt env delete my-project --force         # skip confirmation
```

### Original scrt commands

All original scrt commands still work:

```bash
iscrt set my-key "my-value"       # set a single key
iscrt get my-key                  # get a single key
iscrt list                        # list all keys (flat)
iscrt unset my-key                # remove a key
```

## How It Works

- Secrets are encrypted with **AES-256-GCM** using a key derived from your password via **Argon2id**
- All encryption happens locally -- nothing leaves your machine
- Project organization uses key prefixes: `project-name/SECRET_KEY`
- The `env` commands load the store once, perform all operations in memory, then save once -- fast even with hundreds of secrets

## `.env` File Format

The parser handles:
- `KEY=VALUE` pairs
- Comments (`# ...`) and blank lines
- Quoted values: `KEY="value"` and `KEY='value'`
- `export` prefix: `export KEY=value`
- Values containing `=`: `KEY=base64data==`

## Storage Backends

Inherited from scrt:
- **Local filesystem** (default) -- encrypted file on disk
- **AWS S3** -- encrypted file in an S3 bucket
- **Git** -- encrypted file in a git repository

## License

Apache-2.0 (same as upstream [scrt](https://github.com/loderunner/scrt))
