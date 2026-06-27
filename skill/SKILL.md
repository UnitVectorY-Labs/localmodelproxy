---
name: localmodelproxy
description: Use this skill when working with the localmodelproxy command line application and you need to understand what the command does, what kind of configuration it expects, and how its local OpenAI-compatible proxy behavior works.
---

# localmodelproxy

`localmodelproxy` is a command line application that runs a local OpenAI-compatible proxy. It gives local tools one local /v1 endpoint while the proxy handles the details of routing requests to configured model backends.

The primary benefit here being it logs metrics about the individual requests and can simplify the authentication and routing of requests to separate upstream model backends. It can also rewrite model names for upstream providers, and it can provide a local /v1 endpoint for tools that expect an OpenAI-compatible API.

## Mental model

`localmodelproxy` sits between an OpenAI-compatible client and one or more upstream model backends.

A client sends an OpenAI-style request to the local proxy. The proxy reads the requested model name, chooses a configured backend, applies that backend’s authentication, optionally rewrites the model name for the upstream provider, forwards the request, then records usage information from the response.

Do not assume that localmodelproxy is running all the time, this is an application that is intended to be run on-demand.

The usual local base URL is:

```
http://127.0.0.1:8080/v1
```

A client may still require an API key field, but the value supplied by the client is not the upstream credential. Upstream credentials come from the `localmodelproxy` configuration.

## API shape

The proxy is intentionally narrow. It exposes:

```
GET  /healthz
GET  /v1/models
POST /v1/chat/completions
```

It is not a general OpenAI API mirror. Do not assume other OpenAI endpoints are supported just because the base URL is OpenAI-compatible.

`/v1/models` returns explicitly configured model IDs. A backend using `models: all` can still route chat completion requests for arbitrary model names, but those pass-through model names are not listed by `/v1/models`.

## Configuration concept

`localmodelproxy` is driven primarily by a YAML configuration file whose default location is `~/.localmodelproxy`. The config describes three ideas:

1. Where the local proxy listens.
2. Which upstream backends exist.
3. Which model names route to which backend and how each backend authenticates.

The app can run with defaults, but useful proxy behavior requires configured backends.

A backend defines:

* a unique backend name
* a backend type
* an upstream base URL or Google Cloud project/location information
* an authentication method
* the model IDs served by that backend

The local server host is constrained to loopback addresses. The proxy is designed as a local endpoint, not as an externally exposed server.

## Backend types

There are two backend types.

`openai_compatible` means the upstream backend already exposes an OpenAI-compatible API. This backend type uses a configured `base_url`. This applies to both the official OpenAI API or other local LLM compatible servers like `llama.cpp` or `vllm`.

`gcp_openai` means the backend is a Google Cloud Vertex AI OpenAI-compatible endpoint. It can derive the upstream URL from Google Cloud project and location settings. When using this backend type, a local model ID without a provider prefix is sent upstream with a `google/` prefix unless an explicit `upstream_id` is configured. The benefit of this backend type is that it can use Google Application Default Credentials (ADC) for authentication, which is convenient for local development and testing.

## Model routing

Models are the routing key.

A backend can list explicit model IDs:

```yaml
models:
  - id: local-model-name
```

A model can also define an upstream alias:

```yaml
models:
  - id: local-model-name
    upstream_id: provider/model-name
```

In that case, clients request `local-model-name`, while the proxy sends `provider/model-name` upstream.

A backend can also accept all model names:

```
models: all
```

Routing prefers exact configured model IDs first. A `models: all` backend acts as a fallback. If no backend matches the request model, the proxy returns an OpenAI-style `model_not_found` error.

## Authentication model

Authentication is configured per backend. Supported auth modes are as follows.

None means no upstream authentication is applied. This is useful for local LLM servers that do not require credentials.

```yaml
auth:
  type: none
```

Bearer means the proxy reads a configured token and sets the upstream Authorization header such as an API key provided by OpenAI or other OpenAI-compatible providers.

```yaml
auth:
  type: bearer
  token: ${TOKEN_ENV_VAR}
```

Google ADC means the proxy uses Google Application Default Credentials (ADC) to obtain an access token for the upstream request. This uses the standard methods for obtaining ADC tokens, such as environment variables, well-known file locations, or metadata servers. This is convenient for local development and testing with Google Cloud Vertex AI OpenAI-compatible endpoints.

```yaml
auth:
  type: google_adc
```

OAuth client credentials means the proxy uses a configured OAuth client ID and secret to obtain an access token for the upstream request. This is useful for upstream providers that require OAuth 2.0 client credentials flow.

```yaml
auth:
  type: oauth_client_credentials
  token_url: https://example.com/oauth/token
  client_id: example-client-id
  client_secret: example-client-secret
```

Inbound client authorization is not forwarded as-is. When a backend requires a token, the proxy obtains or reads the configured upstream token and sets the upstream Authorization header itself.

Configuration fields that hold sensitive strings can use environment variable expansion with ${NAME}.

## Configuration Reference

The `localmodelproxy` command line application is open source and available on GitHub.com at https://github.com/UnitVectorY-Labs/localmodelproxy and the most up-to-date documentation for the command line usage is available at https://github.com/UnitVectorY-Labs/localmodelproxy/blob/main/docs/USAGE.md

Use localmodelproxy `--help` for command syntax and available flags. Use the configuration and observed HTTP behavior to understand routing and backend behavior.

## Terminal behavior

When stdout is an interactive terminal, localmodelproxy displays a terminal UI for live stats and optional test requests.

When stdout is not interactive, it prints plain startup and summary output. For AI coding agents, plain output is usually easier to capture and reason about than the interactive UI.

The terminal UI is not required for the proxy API to work. Therefore if an agent is running the proxy the recommendation is to use the `--headless` flag to avoid loading the interactive terminal UI.
