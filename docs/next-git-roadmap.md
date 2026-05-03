# Roadmap to Next Git

Pebble's mission: replace Git with a binary-first VCS that handles large assets at scale.

## Success Metrics

| Metric | Target (Phase 1) | Target (Phase 2) | Target (Phase 3) |
|--------|------------------|------------------|------------------|
| Binarystorage vs Git | <50% of Git size | <30% | <20% |
| Clone time (100GB repo) | N/A | <10min | <5min |
| Push latency (1GB) | <30s | <10s | <5s |
| Command parity | 5 core commands | 10 commands | Full CLI |

---

## Phase 1: Core VCS (Months 1-6)

### Deliverables

1. **Snapshot System**
   - Immutable commits (SHA-256 content-addressed)
   - Full filesystem snapshots
   - File index + metadata generation

2. **ROCK Binary Chunking**
   - FastCDC content-defined chunking
   - Chunk deduplication
   - Reference counting

3. **Storage Layer**
   - SQLite metadata database
   - Two-tier cache (memory + disk)
   - Garbage collection

4. **CLI Commands**
   - `pebble init` — initialize repo
   - `pebble commit` — create snapshot
   - `pebble status` — show changes
   - `pebble log` — view history
   - `pebble checkout` — switch state

5. **Remote Sync (MVP)**
   - Basic HTTP push/pull
   - Object transfer via content-address

### Exit Criteria

- Local commits work reliably
- 10MB binary file stores in <10 chunks
- All unit tests pass

---

## Phase 2: Collaboration (Months 7-12)

### Deliverables

1. **Delta Snapshots**
   - Store only diffs between snapshots
   - Delta chain reconstruction

2. **Enhanced Remote Sync**
   - HTTP/2 multiplexing
   - Resumable transfers
   - SSH + token authentication

3. **Git Interoperability**
   - `pebble git import` — convert Git repo
   - `pebble git export` — convert to Git

4. **Conflict Resolution**
   - Materialized conflict files (no `<<<<<<`)
   - Resolution tracking

5. **Branch Support**
   - Create, list, delete branches
   - Branch merging

### Exit Criteria

- Push 1GB repo over HTTP in <30s
- Import existing Git repo without data loss
- Rebase/merge produces clean history

---

## Phase 3: Enterprise (Months 13-18)

### Deliverables

1. **Access Control**
   - Role-based access control (RBAC)
   - Per-branch permissions

2. **Tiered Storage**
   - Hot/warm/cold object tiers
   - Policy-based retention

3. **Server**
   - Self-hosted Pebble server
   - Authentication middleware

4. **IDE Integrations**
   - VS Code extension
   - IntelliJ plugin

5. **Performance**
   - Benchmark-driven optimization
   - Profile-guided tuning

### Exit Criteria

- RBAC blocks unauthorized pushes
- Server handles 100 concurrent users
- IDE plugins pass community review

---

## Technical Constraints

- **Go 1.21+** — minimum version
- **SQLite** — embedded metadata store
- **SHA-256** — content addressing
- **HTTP/2** — transport protocol
- **No external services** — fully self-hosted