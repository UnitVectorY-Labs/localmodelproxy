[![GitHub release](https://img.shields.io/github/release/UnitVectorY-Labs/localmodelproxy.svg)](https://github.com/UnitVectorY-Labs/localmodelproxy/releases/latest) [![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/licenses/MIT) [![Active](https://img.shields.io/badge/Status-Active-green)](https://guide.unitvectorylabs.com/bestpractices/status/#active) 

# localmodelproxy

Local OpenAI-compatible proxy for routing model requests across configurable backends while handling credentials and tracking token usage.

## Overview

`localmodelproxy` gives OpenAI-compatible clients one local endpoint (`http://127.0.0.1:8080/v1`) while it routes chat-completion requests to configured local or hosted model backends. It monitors token usage per request and model, with optional cost tracking, while keeping upstream credentials in its YAML configuration and allowing local model names to map to upstream names.

The proxy is designed to run on demand and listens only on loopback addresses. Its supported API surface is deliberately small:

- `GET /healthz`
- `GET /v1/models`
- `POST /v1/chat/completions`

## Quick start

Configure each backend once, then point your clients at one local endpoint. The proxy chooses the backend from the requested model, supplies its configured credentials, and tracks usage across all requests.

Create `~/.localmodelproxy`:

```yaml
backends:
  # Pass through any otherwise-unmatched model to a local OpenAI-compatible server.
  - name: local
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    auth:
      type: none
    models: all

  - name: openai
    type: openai_compatible
    base_url: https://api.openai.com/v1
    auth:
      type: bearer
      token: ${OPENAI_API_KEY}
    models:
      - id: gpt-5.6-terra

  # Uses credentials automatically provided by `gcloud auth application-default login`.
  - name: google
    type: gcp_openai
    project: your-google-cloud-project
    location: global
    # Vertex AI's OpenAI-compatible endpoint does not provide /v1/models.
    model_discovery: false
    auth:
      type: google_adc
    models:
      - id: gemini-3.6-flash
        upstream_id: google/gemini-3.6-flash
```

Set `OPENAI_API_KEY` to your OpenAI API key, run `localmodelproxy --headless`, then configure your client to use `http://127.0.0.1:8080/v1`. See the [installation guide](docs/INSTALL.md), [usage reference](docs/USAGE.md), and [configuration examples](docs/EXAMPLES.md) for backend setup, authentication, aliases, and usage tracking.
