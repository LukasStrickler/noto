# ADR 0001: Local-First Modular Architecture

Date: 2026-04-24

## Status

Accepted for planning.

V1 implements local capture, TUI, artifacts, provider transcription, summaries,
and search. Sync, hosted API, and hosted provider routing are later phases of
this architecture.

## Context

Noto must record meetings on macOS, work without hosted infrastructure, produce
portable artifacts, and support future SaaS or self-hosted team deployments
without splitting sync into separate product architectures.

## Decision

Use a local-first modular architecture:

- The native macOS capture helper owns permissions, split mic/system capture, and minimal recording state.
- `noto` owns CLI/TUI workflows, local jobs, artifact writes, processors, search, and providers.
- `~/Noto` stores durable JSON, Markdown, checksums, prompts, and local indexes.
- SQLite FTS5 is a rebuildable local search cache.
- Replaceable processors transform inputs into standard Noto artifacts.
- Post-V1, a single sync/control gateway owns storage access, policy checks, manifest updates, and job submission.
- The gateway can run as an in-process local pass-through or as a remote Noto API.
- The remote Noto API adds auth, device policy, signed object access, provider routing, audit, and billing.

```mermaid
flowchart TD
    classDef captureNode fill:#ede9fe,stroke:#7c3aed,stroke-width:2px,color:#111827;
    classDef appNode fill:#e0f2fe,stroke:#0284c7,stroke-width:2px,color:#111827;
    classDef procNode fill:#dcfce7,stroke:#16a34a,stroke-width:2px,color:#111827;
    classDef artifactNode fill:#ffffff,stroke:#4b5563,stroke-width:1px,color:#111827;
    classDef gatewayNode fill:#fef3c7,stroke:#d97706,stroke-width:2px,color:#111827;
    classDef remoteNode fill:#fee2e2,stroke:#dc2626,stroke-width:2px,color:#111827;

    subgraph Capture["Capture boundary"]
        APP["native capture helper<br/>permissions + split recording"]:::captureNode
    end

    subgraph LocalCore["Local core"]
        CLI["noto CLI/TUI<br/>jobs + UX"]:::appNode
        ART[(Standard Noto artifacts<br/>meeting/transcript/summary)]:::artifactNode
        FTS[(SQLite FTS5<br/>rebuildable cache)]:::artifactNode
    end

    subgraph Processors["Swappable processors"]
        REG["Processor registry<br/>select by config"]:::procNode
        AUDIO["AudioProcessor<br/>prepare audio"]:::procNode
        STT["TranscriptionProcessor<br/>normalize STT"]:::procNode
        SUM["SummaryProcessor<br/>evidence-linked JSON"]:::procNode
        RENDER["Render/Index processors<br/>Markdown + search rows"]:::procNode
    end

    subgraph Gateway["SyncGateway port"]
        GW["SyncGateway<br/>same methods in every mode"]:::gatewayNode
        LOCAL["LocalSyncGateway<br/>in-process pass-through"]:::gatewayNode
        REMOTE["RemoteSyncGateway<br/>HTTP adapter"]:::gatewayNode
    end

    subgraph RemoteServices["Optional remote services"]
        API["Noto API<br/>policy + device trust"]:::remoteNode
        OBJ[(Object storage<br/>R2/S3)]:::artifactNode
        DB[(Metadata DB<br/>audit + billing)]:::artifactNode
        WORK["Async workers<br/>hosted providers"]:::remoteNode
    end

    APP -->|Unix socket| CLI
    CLI -->|Create/update| ART
    CLI -->|Run configured processor| REG
    REG --> AUDIO
    AUDIO --> STT
    STT --> SUM
    STT --> RENDER
    SUM --> RENDER
    RENDER -->|Write standard outputs| ART
    ART -->|Rebuild| FTS
    CLI -->|Sync function calls| GW
    GW -->|local/object-store mode| LOCAL
    GW -->|hosted/self-hosted mode| REMOTE
    LOCAL -->|filesystem or owner credentials| OBJ
    REMOTE -->|HTTP API| API
    API -->|signed object access| OBJ
    API -->|metadata| DB
    API -->|enqueue jobs| WORK
    WORK -->|normalized results| OBJ

    linkStyle 0 stroke:#7c3aed,stroke-width:2px;
    linkStyle 1 stroke:#0284c7,stroke-width:2px;
    linkStyle 2 stroke:#16a34a,stroke-width:2px;
    linkStyle 3 stroke:#16a34a,stroke-width:2px;
    linkStyle 4 stroke:#16a34a,stroke-width:2px;
    linkStyle 5 stroke:#16a34a,stroke-width:2px;
    linkStyle 6 stroke:#16a34a,stroke-width:2px;
    linkStyle 7 stroke:#16a34a,stroke-width:2px;
    linkStyle 8 stroke:#4b5563,stroke-width:2px;
    linkStyle 9 stroke:#4b5563,stroke-width:2px;
    linkStyle 10 stroke:#d97706,stroke-width:2px;
    linkStyle 11 stroke:#d97706,stroke-width:2px;
    linkStyle 12 stroke:#d97706,stroke-width:2px;
    linkStyle 13 stroke:#4b5563,stroke-width:2px;
    linkStyle 14 stroke:#dc2626,stroke-width:2px;
    linkStyle 15 stroke:#dc2626,stroke-width:2px;
    linkStyle 16 stroke:#dc2626,stroke-width:2px;
    linkStyle 17 stroke:#dc2626,stroke-width:2px;
    linkStyle 18 stroke:#dc2626,stroke-width:2px;
```

## Consequences

- Local use never depends on SaaS availability.
- Local, hosted, and self-hosted modes reuse the same sync workflow and artifact contract.
- Providers and processors can be swapped without changing downstream artifact consumers.
- Remote API request handlers can stay lightweight because large artifacts go directly to object storage.
- More adapter boundaries are required early: capture, artifact store, providers, and future sync/control gateway.

## Rejected Alternatives

| Alternative                  | Reason                                                                                                                               |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| TUI-only recorder            | Weak macOS permission UX and fragile recording lifetime.                                                                             |
| Required cloud backend       | Breaks local-first and offline use.                                                                                                  |
| R2 as database               | Object storage is not a query, lock, or patch system.                                                                                |
| Electron app                 | Heavy and still needs a native capture bridge.                                                                                       |
| Live-first transcript system | Adds streaming complexity before post-meeting value is proven.                                                                       |
| Supabase as default backend  | Useful for prototypes, but a small Noto API plus Postgres/object storage gives clearer tenancy, billing, audit, and export behavior. |

## References

- [Apple ScreenCaptureKit capture sample](https://developer.apple.com/documentation/ScreenCaptureKit/capturing-screen-content-in-macos)
- [Cloudflare R2 consistency](https://developers.cloudflare.com/r2/reference/consistency/)
- [Cloudflare R2 presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)
- [Cloudflare Workers limits](https://developers.cloudflare.com/workers/platform/limits/)
- [Supabase self-hosting](https://supabase.com/docs/guides/self-hosting)
