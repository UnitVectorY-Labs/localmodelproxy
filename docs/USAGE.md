---
layout: default
title: Usage
nav_order: 3
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

Override it with `--config` or `LOCALMODELPROXY_CONFIG`.

## Flags

| Flag | Argument | Notes |
|------|----------|-------|
| `--config` | path | YAML config path. Overrides `LOCALMODELPROXY_CONFIG` and the default `~/.localmodelproxy`. |
| `--log` | path | Appends request and response payload logs to the specified file. |
| `--headless` | | Skips the interactive TUI and runs the proxy silently in the foreground. The process continues running until interrupted |
| `--version` | | Prints version and exits. |
| `--help` | | Prints help and exits. |

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `LOCALMODELPROXY_CONFIG` | Config file path when `--config` is not provided. |
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
  recent_requests: 10
  test:
    enabled: true
    system_message: "You are a helpful assistant."
    user_message: "Reply with a short test message to confirm this connection is working."

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
| `ui.recent_requests` | no | `10` | Number of model request rows to show in the TUI recent list. Set to `0` to hide it. Maximum `100`. |
| `ui.test.enabled` | no | `true` | Enables the Test tab in the TUI for sending test requests to models. Set to `false` to disable. |
| `ui.test.system_message` | no | `"You are a helpful assistant."` | System message sent with test requests. |
| `ui.test.user_message` | no | `"Reply with a short test message to confirm this connection is working."` | User message sent with test requests. |

The app uses the TUI when stdout is an interactive terminal and `--headless` is not set. When stdout is not a terminal, it prints plain startup and summary lines. When `--headless` is provided, all UI output is suppressed and the proxy runs silently until interrupted.

### TUI Navigation

The TUI has the following views, accessible with **Tab**, **←/→**, or **h/l**:

- **Stats** – Displays per-model statistics and recent requests (the default view).
- **Models** – Queries each enabled upstream backend's `/models` endpoint and compares the response with the configured models.
- **Test** – Lists all configured models. Use **↑/↓** to select a model and **Enter** to send a test request. The response is displayed inline.

Test requests go through the proxy like any other request, so they count towards stats and token usage.

### Model Discovery Diagnostics

Opening the **Models** tab calls the OpenAI-compatible `/models` endpoint on each enabled upstream backend. The request uses the backend's configured base URL, authentication, HTTP client, and TLS settings. It does not compare against the proxy's own `GET /v1/models` endpoint, because that endpoint is generated from the config itself. Set a backend's `model_discovery: false` when its provider does not support the endpoint; the TUI shows it as disabled instead of sending a request.

The combined list distinguishes the source and consistency of every model:

| Color | Status | Meaning |
|-------|--------|---------|
| Green | `MATCH` | A configured model's effective upstream ID was returned by that backend. |
| Red | `MISSING` | The backend responded successfully but did not return the configured upstream ID. |
| Cyan | `UNCONFIGURED` | The backend returned a model that is not explicitly present in its config. |
| Green | `ALLOWED` | A response model is accepted by a backend configured with `models: all`. |
| Yellow | `UNKNOWN` | Discovery failed, so whether the configured model exists cannot be determined. |

With color enabled, row colors carry the status and the legend shows colored descriptions without repeating color names. When `NO_COLOR` is set, the list adds a textual **Status** column instead.

For configured aliases, matching uses `upstream_id`; when it is omitted, matching uses the local `id`. Use **↑/↓** to select a row and **Enter** to open its detail page. The detail page includes the complete JSON object returned for that model, including provider-specific fields. Use **Esc** or **Backspace** to return to the list, and **R** to query all backends again. Backend-level HTTP, authentication, parsing, and connection failures are shown above the list.

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
| `model_discovery` | no | Whether the Models TUI tab queries this backend's upstream `/v1/models` endpoint. Defaults to `true`; set to `false` for providers that do not support it. |
| `auth` | yes | Auth configuration. |
| `models` | yes | `all` or a list of model entries. |

When `insecure_skip_verify: true`, the app prints a startup warning. This is intended only for local debugging and self-signed development endpoints.

## Models

Backends can expose specific models:

```yaml
models:
  - id: local-model
    upstream_id: provider/model-name
    cost:
      input_per_million: 0.30
      output_per_million: 2.50
      cache_per_million: 0.075
```

Or pass through all models:

```yaml
models: all
```

Routing rules:

- Exact configured model IDs win.
- `models: all` acts as a fallback backend.
- If more than one backend uses `models: all`, config order decides.
- If no backend matches, the proxy returns an OpenAI-style 400 `model_not_found` error.
- `cost` is optional. When present, values are interpreted as USD per 1 million tokens and are shown per request, per model, and in total.

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

- uncached input tokens
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

When cached tokens are reported, they are subtracted from the input token count before display and cost calculation so cached input is not double-counted.

## Request/Response Log

Use `--log PATH` to append request payloads, response payloads, streaming response chunks, and pre-forward failures to a file while keeping the TUI active.

Request log entries are timestamped and include the model, backend, and full upstream token value. Store the log file accordingly.
