---
name: setup
description: >
  Development environment setup from zero — prerequisites, clone, NATS server,
  environment variables, code generation, and running the service. Use for
  getting started, first-time setup, broken environments, or when the service
  won't start. Covers both local mode (fast iteration, no Kubernetes) and E2E
  mode (full stack with Traefik, Heimdall, OpenFGA via OrbStack).
allowed-tools: Bash, Read, Glob, Grep, AskUserQuestion
---

# Development Environment Setup

You are helping a contributor set up the LFX V2 Committee Service for local development. Walk through each step interactively, verifying success before moving on. Explain what each step does in plain language — avoid jargon where possible.

## What This Service Is

The committee service is the backend that manages committees and committee members for the LFX platform. It stores data in NATS (a fast messaging system that also acts as a key-value database) and exposes a REST API.

## Step 1: Choose Your Development Mode

Before setting up anything, ask the contributor which mode they need:

> "There are two ways to run this service locally. Which fits what you're trying to do?
>
> **Local mode** — Run the Go binary directly on your machine. Auth is mocked out, so you don't need any Kubernetes infrastructure. Fast to start, fast to iterate. Best for: features and bug fixes that are self-contained within this service — no dependencies on other LFX platform services.
>
> **E2E mode** — Deploy the service to a local Kubernetes cluster (OrbStack) alongside the full LFX platform stack: Traefik for routing, Heimdall for authentication, OpenFGA for authorization, and real NATS. Best for: features that interact with other platform services, testing auth/authz flows, or validating Helm chart changes before a PR."

Once they've chosen, follow the relevant path below. Steps 2–5 are common to both modes.

---

## Steps 2–5: Common Setup (Both Modes)

### Step 2: Prerequisites

Check that the following tools are installed:

#### Go 1.24+

```bash
go version
```

Go is the programming language this service is written in. If missing, install from [go.dev/doc/install](https://go.dev/doc/install) and select version 1.24 or newer.

#### Git

```bash
git --version
```

If missing, install from [git-scm.com](https://git-scm.com).

#### GitHub CLI (gh)

```bash
gh --version
```

Used for working with pull requests. If missing: [cli.github.com](https://cli.github.com). After installing, run `gh auth login` to authenticate.

#### NATS CLI

```bash
nats --version
```

The NATS command-line tool. On macOS: `brew install nats-io/nats-tools/nats`.

### Step 3: Clone the Repository

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

### Step 4: Install Go Dependencies

```bash
make setup
make deps
```

- `make setup` downloads all Go packages the service needs
- `make deps` installs the Goa code generation tool (more on this in the develop skill)

Verify both completed without errors.

### Step 5: Install Development Tools

```bash
make setup-dev
```

This installs the linter used to check code quality.

---

## Local Mode Setup

Follow this path if you chose **local mode** in Step 1.

### Local Step 1: Additional Prerequisites

#### NATS Server

```bash
nats-server --version
```

NATS is the database this service uses locally. If missing, on macOS: `brew install nats-server`.

### Local Step 2: Environment Variables

The service reads its configuration from a `.env` file. An example file is provided — copy it to create your own local config:

```bash
cp .env.example .env
```

> **Why copy instead of using it directly?** `.env` is gitignored so you can modify it freely (add secrets, change settings) without accidentally pushing those changes. Never edit `.env.example` with real credentials.

Load the environment:
```bash
source .env
```

Open `.env` to see what each variable does — every line has a comment explaining it. The defaults are pre-configured for local mode with auth mocked out, so you can call any endpoint without real credentials.

Verify the key variables are set:
```bash
echo "NATS_URL: $NATS_URL"
echo "AUTH_SOURCE: $AUTH_SOURCE"
echo "JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL: $JWT_AUTH_DISABLED_MOCK_LOCAL_PRINCIPAL"
```

### Local Step 3: Start NATS

The service needs NATS running as both a message broker and key-value database. Open a new terminal and start it:

```bash
nats-server -js
```

The `-js` flag enables JetStream, NATS's persistence feature (required for key-value storage). Leave this terminal open — the server runs in the foreground.

Now, in the original terminal, override the NATS URL to point to your local instance:
```bash
export NATS_URL=nats://localhost:4222
```

### Local Step 4: Create NATS Key-Value Buckets

The service uses buckets in NATS to store data — think of each as a table in a traditional database. Create them:

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

Verify:
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

### Local Step 5: Generate API Code

```bash
make apigen
```

This reads the design files in `cmd/committee-api/design/` and generates the HTTP layer in `gen/`. Think of it like a compiler for your API definition.

### Local Step 6: Build and Run

```bash
make run
```

This builds the Go binary and starts the service. You should see log output indicating it started successfully.

### Local Step 7: Verify

```bash
curl http://localhost:8080/livez
```

Expected response: `OK`

Once you see `OK`, the service is running and you're ready to develop. **Next step:** Use `/develop` to build or modify a feature.

### Local Mode Troubleshooting

**"connection refused" when running the service:**

- Make sure NATS is running (`nats-server -js` in a separate terminal)
- Check `$NATS_URL` is set to `nats://localhost:4222`

**NATS bucket already exists error:**

- That's fine — the buckets already exist from a previous run. Continue.

**Build errors after `make run`:**

- Run `make apigen` first, then `make run`
- If errors persist, run `make setup` to re-download dependencies

**Port 8080 already in use:**

- Find the process: `lsof -i :8080`
- Either stop it or run on a different port: `export PORT=8081`

---

## E2E Mode Setup

Follow this path if you chose **E2E mode** in Step 1. This runs the service inside a local Kubernetes cluster alongside the full LFX platform stack.

### E2E Step 1: Additional Prerequisites

#### OrbStack

OrbStack provides a lightweight local Kubernetes cluster on macOS. Install from [orbstack.dev](https://orbstack.dev), then enable Kubernetes in OrbStack's settings (Settings → Kubernetes → Enable).

Verify:

```bash
kubectl get nodes
```

You should see a single node in `Ready` state.

#### Helm

```bash
helm version
```

Helm is the package manager for Kubernetes — it's how we deploy the service and its dependencies. If missing: `brew install helm`.

### E2E Step 2: Install the LFX Platform Chart

The committee service depends on a shared platform chart that installs Traefik (routing), Heimdall (authentication), OpenFGA (authorization), NATS, and OpenSearch into your cluster.

Create the namespace and install the platform chart from the OCI registry:

```bash
kubectl create namespace lfx

helm install -n lfx lfx-platform \
  oci://ghcr.io/linuxfoundation/lfx-v2-helm/chart/lfx-platform \
  --version 0.1.1 \
  --set lfx-v2-committee-service.enabled=false
```

> **Why `--set lfx-v2-committee-service.enabled=false`?** The platform chart includes the committee service as a subchart. We disable it here so we can deploy our own local version from this repo instead — otherwise there would be two conflicting committee service deployments in the cluster.

Wait for all pods to be ready:
```bash
kubectl get pods -n lfx --watch
```

All pods should reach `Running` status before continuing. This may take a few minutes on first install as images are pulled.

### E2E Step 3: Generate API Code

```bash
make apigen
```

Same as local mode — generates the HTTP layer from the Goa design files.

### E2E Step 4: Deploy the Committee Service

Deploy the service chart from this repo into the cluster:

```bash
make helm-install
```

This runs `helm upgrade --install lfx-v2-committee-service ./charts/lfx-v2-committee-service --namespace lfx`.

Wait for the committee service pod to be ready:
```bash
kubectl get pods -n lfx -l app.kubernetes.io/name=lfx-v2-committee-service --watch
```

### E2E Step 5: Verify

Check the service is reachable through Traefik:
```bash
curl http://committees.k8s.orb.local/livez
```

Expected response: `OK`

> **What's different from local mode:** Requests now go through Traefik (the API gateway) and Heimdall (the auth middleware). Calls to most endpoints require a real JWT token. Check the platform documentation for how to obtain a token for local testing.

### E2E Step 6: Iterating

When you make code changes and want to test them in the cluster:

```bash
make docker-build    # Build a new Docker image
make helm-install    # Redeploy (upgrade) the Helm release
```

To restart the pod after a config change without rebuilding:

```bash
kubectl rollout restart deployment/lfx-v2-committee-service -n lfx
```

To tear down just the committee service (without removing the platform):

```bash
make helm-uninstall
```

### E2E Mode Troubleshooting

**Pods stuck in `Pending`:**

- Check events: `kubectl describe pod <pod-name> -n lfx`
- Usually a resource or image pull issue

**`helm install` fails with "already exists":**

- Use `helm upgrade` instead, or uninstall first: `helm uninstall lfx-platform -n lfx`

**`committees.k8s.orb.local` not resolving:**

- OrbStack automatically handles `.orb.local` DNS — make sure OrbStack is running
- Check Traefik is running: `kubectl get pods -n lfx | grep traefik`

**Auth errors on endpoints:**

- In E2E mode, auth is real. You need a valid JWT. Check the platform docs for obtaining a local token, or switch to local mode if your feature doesn't require cross-service integration.
