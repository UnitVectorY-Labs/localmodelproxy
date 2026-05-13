---
layout: default
title: Install
nav_order: 2
permalink: /install
---

# Installation

## Binary Download

Download the latest release from the [releases page](https://github.com/UnitVectorY-Labs/localmodelproxy/releases).

### Linux (amd64)

```bash
curl -L https://github.com/UnitVectorY-Labs/localmodelproxy/releases/latest/download/localmodelproxy_linux_amd64 -o localmodelproxy
chmod +x localmodelproxy
sudo mv localmodelproxy /usr/local/bin/
```

### macOS (amd64)

```bash
curl -L https://github.com/UnitVectorY-Labs/localmodelproxy/releases/latest/download/localmodelproxy_darwin_amd64 -o localmodelproxy
chmod +x localmodelproxy
sudo mv localmodelproxy /usr/local/bin/
```

### macOS (arm64 / Apple Silicon)

```bash
curl -L https://github.com/UnitVectorY-Labs/localmodelproxy/releases/latest/download/localmodelproxy_darwin_arm64 -o localmodelproxy
chmod +x localmodelproxy
sudo mv localmodelproxy /usr/local/bin/
```

### Windows

Download `localmodelproxy_windows_amd64.exe` from the releases page and add it to your PATH.

## Building from Source

### Build

```bash
git clone https://github.com/UnitVectorY-Labs/localmodelproxy.git
cd localmodelproxy
go build -o localmodelproxy .
```

### Install to GOPATH

```bash
go install github.com/UnitVectorY-Labs/localmodelproxy@latest
```

## Verify Installation

```bash
localmodelproxy --help
```
