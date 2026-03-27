# Backend Implementation Plan (Frontend-Unchanged)

This document is the execution reference for making the backend fully support the current frontend without changing anything under `web/`.

## Scope Lock

- **Do not modify** any files in `web/`.
- **Goal**: implement backend behavior to match existing frontend contracts in `web/src/contracts.ts` and current component/hook usage.
- **Primary deliverables**:
  - HTTP endpoints: `POST /trip/preview`, `POST /trip/start`
  - WebSocket endpoints: `GET /riders`, `GET /drivers`
  - In-memory orchestration for trip creation, driver matching, acceptance/decline flow
  - Payment session event stub for frontend Stripe handoff flow

---

## Frontend Contract Targets (Must Match Exactly)

## HTTP

### `POST /trip/preview`
- Request body:
  - `userID: string`
  - `pickup: { latitude: number, longitude: number }`
  - `destination: { latitude: number, longitude: number }`
- Response shape must be wrapped in API response:
  - `{ data: { route, rideFares } }`
- Route shape expected by frontend:
  - `route.geometry[0].coordinates[]` where each coordinate has `latitude` and `longitude`

### `POST /trip/start`
- Request body:
  - `rideFareID: string`
  - `userID: string`
- Response shape expected by frontend currently:
  - `{ "tripID": "..." }`
- Note: this one is consumed as a direct object (not `data` wrapped) by current frontend code.

## WebSocket

### Rider stream
- URL: `/riders?userID=...`
- Must emit typed envelope messages:
  - `{ "type": "...", "data": ... }`

### Driver stream
- URL: `/drivers?userID=...&packageSlug=...`
- Must emit typed envelope messages:
  - `{ "type": "...", "data": ... }`

## Event Types
Use names already defined in frontend and shared contracts, including:
- `trip.event.created`
- `trip.event.driver_assigned`
- `trip.event.no_drivers_found`
- `driver.cmd.trip_request`
- `driver.cmd.trip_accept`
- `driver.cmd.trip_decline`
- `driver.cmd.location`
- `driver.cmd.register`
- `payment.event.session_created`

---

## Ordered Implementation Phases

## Phase 1 — API Gateway Foundation
Target folder: `services/api-gateway`

### Files to update
- `main.go`
- `types.go`
- `json.go`

### Work
1. In `main.go`:
   - Read server address from `GATEWAY_HTTP_ADDR` with fallback `:8081`.
   - Register handlers for:
     - `POST /trip/preview`
     - `POST /trip/start`
     - `GET /riders`
     - `GET /drivers`
2. In `types.go`:
   - Add backend DTOs matching frontend expected payloads:
     - trip preview request/response
     - start trip request/response
     - route, geometry, coordinate, fare, trip, driver
     - websocket envelope and driver accept/decline payloads
3. In `json.go`:
   - Keep generic `writeJSON`.
   - Add helper for API envelope responses (for preview endpoint), e.g. `writeAPIResponse` using `shared/contracts.APIResponse`.

### Done criteria
- Server boots with all required routes registered.
- Address config uses env key that matches k8s config map (`GATEWAY_HTTP_ADDR`).

---

## Phase 2 — HTTP Endpoints for Rider Flow
Target file: `services/api-gateway/http.go`

### Work
1. Implement `handleTripPreview`:
   - Validate JSON body and required fields.
   - Build a synthetic route from pickup to destination.
   - Compute approximate distance/duration.
   - Create fare options for package slugs:
     - `sedan`, `suv`, `van`, `luxury`
   - Return `{ data: { route, rideFares } }`.
2. Implement `handleTripStart`:
   - Validate `rideFareID` and `userID`.
   - Create trip with unique ID in in-memory store.
   - Return `{ "tripID": "..." }`.
   - Trigger async matching flow (phase 4 behavior).

### Done criteria
- Rider can click map and receive route/fare preview.
- Rider can start trip and receive trip ID.

---

## Phase 3 — WebSocket Transport
Target file(s):
- `services/api-gateway/ws.go` (or split by riders/drivers)
- `services/api-gateway/store.go` (connection/state store)

### Work
1. Add websocket upgrader and connection lifecycle handling.
2. Implement rider handler (`/riders`):
   - validate `userID`
   - register/unregister rider connection
   - accept optional incoming location event payloads
3. Implement driver handler (`/drivers`):
   - validate `userID`, `packageSlug`
   - register/unregister driver connection + metadata
   - emit `driver.cmd.register` on connect
   - process incoming:
     - `driver.cmd.location`
     - `driver.cmd.trip_accept`
     - `driver.cmd.trip_decline`
4. Ensure all outbound messages use envelope `{ type, data }`.

### Done criteria
- Rider and driver clients can connect and receive/submit events.

---

## Phase 4 — In-Memory Matching Orchestration
Target file: `services/api-gateway/matching.go`

### Work
1. On trip start:
   - send rider `trip.event.created`
   - select candidate drivers (initial strategy: connected drivers, optionally filtered by package)
2. If no candidates:
   - send rider `trip.event.no_drivers_found`
3. If candidates exist:
   - send `driver.cmd.trip_request` to candidate driver(s)
4. On first accept:
   - lock trip assignment (first accept wins)
   - send rider `trip.event.driver_assigned`
   - send rider `payment.event.session_created` with stub session payload
5. On decline:
   - try next candidate; if exhausted => `trip.event.no_drivers_found`

### Done criteria
- End-to-end driver accept/decline flow works from existing frontend.

---

## Phase 5 — Optional Extraction to Trip Service (Backend-only)
Target folders:
- `services/trip-service/cmd`
- `services/trip-service/internal/...`
- `services/api-gateway/...` client/adapter code

### Work
1. Turn trip-service into a real long-running server.
2. Add internal endpoints or gRPC for trip CRUD/state updates.
3. Move trip state ownership from gateway store to trip-service.
4. Keep gateway external contract unchanged.

### Done criteria
- Trip lifecycle state is owned by trip-service instead of gateway memory.

---

## Phase 6 — Event Bus (RabbitMQ) Integration
Target areas:
- `services/api-gateway`
- `services/trip-service`
- future `driver-service`, `payment-service`
- `shared/contracts/amqp.go` routing keys

### Work
1. Add publisher/consumer setup.
2. Move orchestration from direct in-memory actions to event/command flows.
3. Keep websocket fanout in gateway.

### Done criteria
- Core trip flow transitions emitted and consumed through RabbitMQ.

---

## File-by-File Work Queue (Immediate)

1. `services/api-gateway/main.go`
2. `services/api-gateway/types.go`
3. `services/api-gateway/json.go`
4. `services/api-gateway/http.go`
5. `services/api-gateway/store.go`
6. `services/api-gateway/ws.go`
7. `services/api-gateway/matching.go`

---

## Validation Checklist (No Frontend Changes)

## Manual API checks

```bash
curl -i -X POST http://localhost:8081/trip/preview \
  -H "Content-Type: application/json" \
  -d '{"userID":"u1","pickup":{"latitude":37.77,"longitude":-122.41},"destination":{"latitude":37.78,"longitude":-122.42}}'
```

```bash
curl -i -X POST http://localhost:8081/trip/start \
  -H "Content-Type: application/json" \
  -d '{"rideFareID":"<id-from-preview>","userID":"u1"}'
```

## Runtime checks
- One rider browser session + one driver session can connect.
- Driver gets `driver.cmd.register` upon WS connect.
- Rider start trip triggers `trip.event.created`.
- Driver gets `driver.cmd.trip_request`.
- Driver accept triggers rider `trip.event.driver_assigned` and then `payment.event.session_created`.
- Decline/no driver path yields `trip.event.no_drivers_found`.

---

## Notes / Constraints

- Keep implementation simple and deterministic first (in-memory storage and mock route/fare calculations).
- Maintain payload compatibility over purity until frontend is fully unblocked.
- Once stable, refactor for separation, persistence, and event-bus architecture.
