---
layout: default
title: Usage
nav_order: 2
permalink: /usage
has_children: true
---

# Usage

`localmodelproxy` follows Unix-style command line conventions and is configured primarily through a YAML file.

```bash
localmodelproxy [OPTIONS]
```

By default, the config file is read from:

```text
~/.localmodelproxy
```

Override it with `--config` or `GEOPENPROXY_CONFIG`.

## Flags

| Flag | Argument | Notes |
|------|----------|-------|
| `--config` | path | YAML config path. Overrides `GEOPENPROXY_CONFIG` and the default `~/.localmodelproxy`. |
| `--host` | host | Local bind host. Defaults to `127.0.0.1`. Non-loopback hosts are rejected. |
| `--port` | port | Local bind port. Defaults to `8080`. |
| `--ui` | mode | `auto`, `tui`, `plain`, or `jsonl`. Defaults to `auto`. |
| `--verbose` | | Enables additional diagnostics. |
| `--version` | | Prints version and exits. |
| `--help` | | Prints help and exits. |

The legacy single-backend flags `--project` and `--location` may be kept for compatibility during migration, but backend configuration should live in YAML.

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `GEOPENPROXY_CONFIG` | Config file path when `--config` is not provided. |
| `GOOGLE_APPLICATION_CREDENTIALS` | Service account JSON file used by Google Application Default Credentials. |
| `GOOGLE_CLOUD_PROJECT` | Optional Google Cloud project fallback for Google ADC backends. |
| `CLOUDSDK_CORE_PROJECT` | Optional Google Cloud project fallback for Google ADC backends. |
| `GOOGLE_CLOUD_LOCATION` | Optional Google Cloud location fallback for Google ADC backends. |
| `GOOGLE_CLOUD_REGION` | Optional Google Cloud location fallback for Google ADC backends. |
| `CLOUDSDK_COMPUTE_REGION` | Optional Google Cloud location fallback for Google ADC backends. |
| `NO_COLOR` | Disables color in terminal output when set to any value. |

Environment variable expansion is supported in sensitive config fields by using `${NAME}`.

## Config Structure

```yaml
server:
  host: 127.0.0.1
  port: 8080

ui:
  mode: auto

backends:
  - name: backend-name
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    insecure_skip_verify: false
    auth:
      type: none
    models:
      - id: local-model-name
```

## Server

| Field | Required | Default | Notes |
|-------|----------|---------|-------|
| `server.host` | no | `127.0.0.1` | Must be loopback. |
| `server.port` | no | `8080` | Local listening port. |

## UI

| Field | Required | Default | Notes |
|-------|----------|---------|-------|
| `ui.mode` | no | `auto` | `auto`, `tui`, `plain`, or `jsonl`. |

`auto` uses the TUI when stdout is an interactive terminal and plain logs otherwise.

## Backends

Each backend declares where requests go, how they authenticate, and which models they serve.

| Field | Required | Notes |
|-------|----------|-------|
| `name` | yes | Unique backend name shown in diagnostics. |
| `type` | yes | `gcp_openai` or `openai_compatible`. |
| `base_url` | yes* | OpenAI-compatible base URL. Required for `openai_compatible`; optional for `gcp_openai` when project/location are provided. |
| `project` | yes* | Google Cloud project for `gcp_openai` when `base_url` is omitted. |
| `location` | yes* | Google Cloud location for `gcp_openai` when `base_url` is omitted. Defaults to `global` when project is configured. |
| `insecure_skip_verify` | no | Disables TLS certificate verification for backend API calls. Defaults to `false`. |
| `auth` | yes | Auth configuration. |
| `models` | yes | `all` or a list of model entries. |

When `insecure_skip_verify: true`, the app prints a startup warning. This is intended only for local debugging and self-signed development endpoints.

## Models

Backends can expose specific models:

```yaml
models:
  - id: local-model
    upstream_id: provider/model-name
```

Or pass through all models:

```yaml
models: all
```

Routing rules:

- Exact configured model IDs win.
- `models: all` acts as a fallback backend.
- If more than one backend uses `models: all`, config order decides.
- If no backend matches, the proxy returns an OpenAI-style 404 error.

## Authentication

### None

Use this for local LLM servers that do not require auth.

```yaml
auth:
  type: none
```

### Bearer Token

Use this when the upstream accepts a fixed bearer token.

```yaml
auth:
  type: bearer
  token: ${UPSTREAM_API_TOKEN}
```

The proxy strips inbound `Authorization` and replaces it with the configured token.

### Google Application Default Credentials

Use this for Google Cloud OpenAI-compatible endpoints.

```yaml
auth:
  type: google_adc
```

Authenticate locally with:

```bash
gcloud auth application-default login
```

Or use a service account:

```bash
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
```

### OAuth Client Credentials

Use this for internal or vendor endpoints that require an OAuth token exchange.

```yaml
auth:
  type: oauth_client_credentials
  token_url: https://auth.example.internal/oauth/token
  client_id: local-client
  client_secret: ${LOCAL_CLIENT_SECRET}
  scopes:
    - models.invoke
  insecure_skip_verify: false
```

| Field | Required | Notes |
|-------|----------|-------|
| `token_url` | yes | OAuth token endpoint. |
| `client_id` | yes | OAuth client ID. |
| `client_secret` | yes | OAuth client secret. Environment expansion is recommended. |
| `scopes` | no | Optional OAuth scopes. |
| `insecure_skip_verify` | no | Disables TLS verification only for token exchange. Defaults to `false`. |

When token exchange TLS verification is disabled, the app prints a startup warning.

## Token Accounting

The TUI and logs track:

- input tokens
- output tokens
- thinking tokens
- cached tokens
- total tokens

The proxy captures standard OpenAI-compatible usage fields such as:

- `prompt_tokens`
- `completion_tokens`
- `total_tokens`
- `prompt_tokens_details.cached_tokens`
- `completion_tokens_details.reasoning_tokens`

It also captures Google-style usage metadata when present.
