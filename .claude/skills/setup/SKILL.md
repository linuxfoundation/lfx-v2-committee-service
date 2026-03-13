---
name: setup
description: >
  Development environment setup from zero — prerequisites, clone, NATS server,
  environment variables, code generation, and running the service. Use for
  getting started, first-time setup, broken environments, or when the service
  won't start.
allowed-tools: Bash, Read, Glob, Grep, AskUserQuestion
---

# Development Environment Setup

You are helping a contributor set up the LFX V2 Committee Service for local development. Walk through each step interactively, verifying success before moving on. Explain what each step does in plain language — avoid jargon where possible.

## What This Service Is

The committee service is the backend that manages committees and committee members for the LFX platform. It stores data in NATS (a fast messaging system that also acts as a key-value database) and exposes a REST API.

## Step 1: Prerequisites

Check that the following tools are installed:

### Go 1.24+
```bash
go version
```
Go is the programming language this service is written in. If missing, instruct them to install it from [go.dev/doc/install](https://go.dev/doc/install) and select version 1.24 or newer.

### Git
```bash
git --version
```
If missing, install from [git-scm.com](https://git-scm.com).

### GitHub CLI (gh)
```bash
gh --version
```
Used for working with pull requests. If missing: [cli.github.com](https://cli.github.com). After installing, run `gh auth login` to authenticate.

### NATS Server
```bash
nats-server --version
```
NATS is the database this service uses. Install from [nats.io](https://docs.nats.io/running-a-nats-service/introduction/installation). On macOS with Homebrew: `brew install nats-server`.

### NATS CLI
```bash
nats --version
```
The NATS command-line tool, used to set up the database buckets. On macOS: `brew install nats-io/nats-tools/nats`.

## Step 2: Clone the Repository

If not already cloned:
```bash
git clone https://github.com/linuxfoundation/lfx-v2-committee-service.git
cd lfx-v2-committee-service
```

If already in the repo, confirm the location:
```bash
pwd
git remote -v
```

## Step 3: Install Go Dependencies

```bash
make setup
make deps
```

- `make setup` downloads all Go packages the service needs
- `make deps` installs the Goa code generation tool (more on this in the develop skill)

Verify both completed without errors.

## Step 4: Install Development Tools

```bash
make setup-dev
```

This installs the linter used to check code quality.

## Step 5: Environment Variables

The service reads its configuration from environment variables. A ready-to-use `.env` file already exists in the repo root — it's pre-configured for local development with auth disabled (so you don't need external credentials to get started).

Load the environment:
```bash
source .env
```

> **What the .env file does:** It sets the service to connect to a local NATS server and disables authentication, so you can test without needing real credentials. Never commit changes to `.env` with real secrets.

Verify the key variables are set:
```bash
echo "NATS_URL: $NATS_URL"
echo "AUTH_SOURCE: $AUTH_SOURCE"
echo "JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL: $JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL"
```

Expected output:
```
NATS_URL: nats://lfx-platform-nats.lfx.svc.cluster.local:4222
AUTH_SOURCE: mock
JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL: project_super_admin
```

> **Note:** The NATS_URL in .env points to the shared cluster — for local development, you'll override this in Step 6.

## Step 6: Start NATS Locally

The service needs NATS running as both a message broker and key-value database. Open a new terminal and start it:

```bash
nats-server -js
```

The `-js` flag enables JetStream, which is NATS's persistence feature (required for key-value storage). Leave this terminal open — the server runs in the foreground.

Now, in the original terminal, override the NATS URL for local development:
```bash
export NATS_URL=nats://localhost:4222
```

## Step 7: Create NATS Key-Value Buckets

The service uses three "buckets" in NATS to store committees, settings, and members. Think of each bucket as a table in a traditional database. Create them:

```bash
nats kv add committees \
  --history=20 --storage=file \
  --max-value-size=10485760 --max-bucket-size=1073741824

nats kv add committee-settings \
  --history=20 --storage=file \
  --max-value-size=10485760 --max-bucket-size=1073741824

nats kv add committee-members \
  --history=20 --storage=file \
  --max-value-size=10485760 --max-bucket-size=1073741824
```

Verify the buckets were created:
```bash
nats kv ls
```

Expected output:
```
committee-members
committee-settings
committees
```

> These buckets only need to be created once. If you restart NATS with the same data directory, they'll still be there.

## Step 8: Generate API Code

This service uses a framework called Goa that generates HTTP server code from a design specification. Run:

```bash
make apigen
```

This reads the design files in `cmd/committee-api/design/` and generates the HTTP layer in `gen/`. Think of it like a compiler for your API definition — you describe what endpoints exist and what data they accept, and Goa writes the boilerplate HTTP code for you.

## Step 9: Build and Run the Service

```bash
make run
```

This builds the Go binary and starts the service. You should see log output indicating it started successfully.

## Step 10: Verify

In a new terminal, check the service is alive:
```bash
curl http://localhost:8080/livez
```

Expected response: `OK`

Also check readiness:
```bash
curl http://localhost:8080/readyz
```

Expected response: `OK`

## Troubleshooting

**"connection refused" when running the service:**
- Make sure NATS is running (`nats-server -js` in a separate terminal)
- Check `$NATS_URL` is set to `nats://localhost:4222`

**NATS bucket already exists error:**
- That's fine — the buckets already exist from a previous run. Continue.

**Build errors after `make run`:**
- Run `make apigen` first, then `make run`
- If errors persist, run `make setup` to re-download dependencies

**Port 8080 already in use:**
- Another process is using the port: `lsof -i :8080` to find it
- Either stop that process or export a different port: `export PORT=8081`

**Lint tool not found:**
- Run `make setup-dev` to install it

## Done

Once `curl http://localhost:8080/livez` returns `OK`, the service is running and you're ready to develop.

**Next step:** Use `/develop` to build or modify a feature.
