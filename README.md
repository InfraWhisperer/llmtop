```
██╗     ██╗     ███╗   ███╗████████╗ ██████╗ ██████╗
██║     ██║     ████╗ ████║╚══██╔══╝██╔═══██╗██╔══██╗
██║     ██║     ██╔████╔██║   ██║   ██║   ██║██████╔╝
██║     ██║     ██║╚██╔╝██║   ██║   ██║   ██║██╔═══╝
███████╗███████╗██║ ╚═╝ ██║   ██║   ╚██████╔╝██║
╚══════╝╚══════╝╚═╝     ╚═╝   ╚═╝    ╚═════╝ ╚═╝
```

<div align="center">

**htop for your LLM inference cluster**

[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue?style=flat-square)](LICENSE)
[![Release](https://img.shields.io/github/v/release/InfraWhisperer/llmtop?style=flat-square)](https://github.com/InfraWhisperer/llmtop/releases)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey?style=flat-square)](https://github.com/InfraWhisperer/llmtop/releases)

Real-time terminal dashboard for **vLLM**, **SGLang**, **LMCache**, **NVIDIA NIM**, and **NVIDIA Dynamo** inference clusters.

</div>

---

<div align="center">
<img src="docs/llmtop-demo.gif" alt="llmtop demo" width="900">
</div>

---

## Install

```bash
brew install InfraWhisperer/tap/llmtop
```

Or grab a binary from [GitHub Releases](https://github.com/InfraWhisperer/llmtop/releases), or:

```bash
go install github.com/InfraWhisperer/llmtop/cmd/llmtop@latest
```

## Quick Start

```bash
# Kubernetes — auto-discovers inference pods via API server proxy
llmtop

# Specific namespace
llmtop -n inference

# Direct endpoints
llmtop -e http://10.0.0.1:8000 -e http://10.0.0.2:8000

# Config file
llmtop --config cluster.yaml

# Snapshot mode
llmtop --once --output json
```

## What It Does

- Real-time KV cache, queue depth, TTFT/ITL latency, token throughput across all workers
- GPU resource view (`g`) — utilization, VRAM, temperature, power via DCGM exporter
- Model-grouped view (`m`) — aggregate stats by model with drill-down
- Kubernetes-native — auto-discovers pods, scrapes through API server proxy, no port-forwards needed
- Works with NVIDIA Dynamo — filters frontends, labels prefill/decode workers automatically

## Backend Support

| Backend | Metrics | Auto-detect | Notes |
|---------|---------|-------------|-------|
| **vLLM** | ✅ Full | ✅ Yes | `vllm:` metric prefix |
| **SGLang** | ✅ Full | ✅ Yes | `sglang:` metric prefix |
| **LMCache** | ✅ Cache | ✅ Yes | `lmcache_` metric prefix |
| **NIM** | ✅ Full | ✅ Yes | Unprefixed vLLM metrics at `/v1/metrics` |
| **Dynamo** | ✅ Full | ✅ Yes | Auto-filters frontends, labels decode/prefill workers |
| **TGI** | ✅ Full | ✅ Yes | `tgi_` metric prefix, no KV cache metrics |
| **TensorRT-LLM** | ✅ Full | ✅ Yes | `trtllm_` prefix at `/prometheus/metrics` |
| **Triton** | ✅ Full | ✅ Yes | `nv_inference_` / `nv_trt_llm_` on port 8002 |
| **llama.cpp** | ✅ Full | ✅ Yes | `llamacpp:` prefix, requires `--metrics` flag |
| **LiteLLM** | ✅ Full | ✅ Yes | `litellm_` prefix, proxy-level metrics |
| **Ollama** | ⚡ Basic | ✅ Yes | JSON `/api/ps` — model name + online status |

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `s` | Cycle sort column |
| `f` | Cycle backend filter |
| `d` | Detail view |
| `g` | GPU view |
| `m` | Model-grouped view |
| `r` | Force refresh |
| `e` | Export JSON |
| `?` | Help |

## Documentation

See [docs/design.md](docs/design.md) for full documentation including config file format, Kubernetes discovery details, RBAC requirements, NVIDIA Dynamo support, GPU monitoring, metrics collected, and architecture.

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).

---

<div align="center">

Built for the LLM inference community.

If llmtop saved you from a 3am KV cache saturation incident, [star the repo](https://github.com/InfraWhisperer/llmtop).

</div>
