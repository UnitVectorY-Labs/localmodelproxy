---
layout: default
title: localmodelproxy
nav_order: 1
permalink: /
---

# localmodelproxy

`localmodelproxy` is a local model proxy for OpenAI-compatible applications.

Run it on localhost, point your tools at one local `/v1` endpoint, and route model requests to the right backend without spreading credentials across every local app. It can front cloud-hosted model APIs, local inference servers, private lab endpoints, and Google Cloud OpenAI-compatible endpoints while keeping token usage visible in a simple terminal dashboard.

The goal is pragmatic: one local endpoint, explicit YAML configuration, predictable routing, and clear token accounting.

## Why it exists

Many tools already know how to talk to OpenAI-compatible APIs, but every backend has different authentication, model naming, local TLS quirks, and usage reporting behavior. `localmodelproxy` centralizes those details behind a localhost-only proxy so client apps stay simple.

Use it when you want to:

- Use local application credentials without exposing them to every tool
- Route different model names to different OpenAI-compatible backends
- Combine local and remote models behind one local base URL
- See input, output, thinking, cached, and total token usage as requests flow through
- Debug local HTTPS backends with explicit, visible unsafe TLS warnings

## Shape of the app

`localmodelproxy` listens on `127.0.0.1` by default and exposes:

- `GET /v1/models` — always returns the configured models list
- `POST /v1/chat/completions` — validated against configured model IDs and forwarded to the matching backend

The proxy selects a backend by inspecting the request model, validates it against configured model IDs (returning a 400 `model_not_found` error if not found), forwards the request transparently, rewrites model aliases when configured, applies the backend's authentication method, and aggregates token usage from the response.

## Documentation

- [Usage](USAGE.md)
- [Install](INSTALL.md)
- [Examples](EXAMPLES.md)
