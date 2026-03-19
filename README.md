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
