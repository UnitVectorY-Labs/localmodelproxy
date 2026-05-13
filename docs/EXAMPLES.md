---
layout: default
title: Examples
nav_order: 3
permalink: /examples
has_children: true
---

# Examples

These examples show common backend configurations.

Run the proxy:

```bash
localmodelproxy
```

Point local OpenAI-compatible clients at:

```text
http://127.0.0.1:8080/v1
```

Most clients still require an API key field. Use any placeholder value unless the client itself validates it.

## Google Cloud OpenAI-Compatible Backend

This backend uses Google Application Default Credentials and exposes a local model name while sending the required publisher-prefixed model name upstream.

```yaml
server:
  host: 127.0.0.1
  port: 8080

ui:
  mode: auto

backends:
  - name: cloud
    type: gcp_openai
    project: example
    location: global
    auth:
      type: google_adc
    models:
      - id: gemini-3.1-flash-lite-preview
        upstream_id: google/gemini-3.1-flash-lite-preview
```

Authenticate:

```bash
gcloud auth application-default login
```

Validate:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.1-flash-lite-preview",
    "messages": [
      {"role": "user", "content": "Reply with one short sentence."}
    ]
  }'
```

## Local LLM Backend With No Auth

Use this for local servers such as Ollama, LM Studio, or any local OpenAI-compatible endpoint that does not require credentials.

```yaml
server:
  host: 127.0.0.1
  port: 8080

backends:
  - name: local
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    auth:
      type: none
    models: all
```

In this setup, any model name requested by the client is forwarded to the local backend.

```bash
curl http://127.0.0.1:8080/v1/models
```

## OpenAI-Compatible Backend With Bearer Token

Use this for any OpenAI-compatible backend that expects a static bearer token.

```yaml
backends:
  - name: hosted
    type: openai_compatible
    base_url: https://api.example.com/v1
    auth:
      type: bearer
      token: ${HOSTED_MODEL_TOKEN}
    models:
      - id: fast-model
      - id: large-model
```

Then set:

```bash
export HOSTED_MODEL_TOKEN=your-token
```

Client requests to `fast-model` or `large-model` route to this backend.

## OAuth Client Credentials Backend

Use this when an upstream service requires a client credentials token exchange before model calls.

```yaml
backends:
  - name: internal
    type: openai_compatible
    base_url: https://models.internal.example/v1
    auth:
      type: oauth_client_credentials
      token_url: https://auth.internal.example/oauth/token
      client_id: local-model-client
      client_secret: ${INTERNAL_MODEL_CLIENT_SECRET}
      scopes:
        - models.invoke
    models:
      - id: internal-small
      - id: internal-large
```

Then set:

```bash
export INTERNAL_MODEL_CLIENT_SECRET=secret-value
```

The proxy handles token exchange and caches the resulting access token until it expires.

## Local HTTPS Debug Backend With Invalid Certificates

Use `insecure_skip_verify` only for local debugging with self-signed or otherwise invalid certificates.

```yaml
backends:
  - name: localtls
    type: openai_compatible
    base_url: https://localhost:8443/v1
    insecure_skip_verify: true
    auth:
      type: none
    models: all
```

At startup, the proxy prints a warning that TLS certificate verification is disabled for this backend.

## OAuth Token Exchange With Invalid Certificates

Backend calls and token exchange can be controlled separately.

```yaml
backends:
  - name: oauthdebug
    type: openai_compatible
    base_url: https://models.local.test/v1
    insecure_skip_verify: false
    auth:
      type: oauth_client_credentials
      token_url: https://auth.local.test/oauth/token
      client_id: debug-client
      client_secret: ${DEBUG_CLIENT_SECRET}
      insecure_skip_verify: true
    models:
      - id: debug-model
```

This keeps model API TLS verification enabled while allowing a local debug token server with an invalid certificate.

## Multiple Backends Together

This configuration combines Google Cloud, a local no-auth backend, and a hosted bearer-token backend.

```yaml
server:
  host: 127.0.0.1
  port: 8080

ui:
  mode: auto

backends:
  - name: cloud
    type: gcp_openai
    project: example
    location: global
    auth:
      type: google_adc
    models:
      - id: gemini-3.1-flash-lite-preview
        upstream_id: google/gemini-3.1-flash-lite-preview

  - name: hosted
    type: openai_compatible
    base_url: https://api.example.com/v1
    auth:
      type: bearer
      token: ${HOSTED_MODEL_TOKEN}
    models:
      - id: hosted-small
      - id: hosted-large

  - name: local
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    auth:
      type: none
    models: all
```

Routing behavior:

- `gemini-3.1-flash-lite-preview` routes to `cloud`
- `hosted-small` and `hosted-large` route to `hosted`
- any other model routes to `local` because it uses `models: all`
