# Frontend-Driven Message History Loading with Virtual List

## Goal Description
Implement a high-performance, frontend-driven message history loading mechanism. The frontend will proactively request historical messages via WebSocket, utilizing a cursor-based pagination API (using timestamp/message_id). To ensure UI performance, the frontend will employ a virtual list (or sliding window cache) to manage DOM nodes efficiently, accompanied by user experience enhancements like scroll anchoring and a "new message" floating button.

## Proposed Changes

### Backend (Golang)
- **`internal/protocol.go`**: Define a new WebSocket `ControlMessage` type `fetch_history`. Define a new `EventMessage` type `history_sync` to send historical messages back to the frontend.
- **`internal/session.go`**: 
  - Handle the `fetch_history` control message.
  - Query messages from `sqliteStore.LoadContextBefore` (needs to be implemented).
  - Send the `history_sync` event containing the requested messages and a `has_more` flag.
- **`internal/sqlite_store.go`**:
  - Implement `LoadContextBefore(cursor string, limit int) ([]ChatMessage, error)` to support cursor-based pagination (using `created_at_ms` as the primary cursor, potentially combined with `message_id` for exact ties).

### Frontend (JavaScript/WebUI)
- **`webui/page/chat/config-store.js` & `config-drawer.js`**:
  - Add `initialHistorySize` (default: 5) and `pullHistorySize` (default: 10) configurations.
- **`webui/page/chat/chat-store.js`**:
  - State management for `hasMoreHistory`, `isFetchingHistory`.
  - Buffer for storing the loaded history.
- **`webui/page/chat/event-router.js` & `session-controller.js`**:
  - Send `fetch_history` when WebSocket connects.
  - Handle incoming `history_sync` events and merge them into the local store.
- **`webui/page/chat/index.html` & UI Logic (Virtual List/Scroll Management)**:
  - Implement scroll event listener with throttling.
  - Implement scroll anchoring logic when prepending history nodes.
  - Implement the "slide window" DOM management (keep DOM nodes around `5 * pullHistorySize` up and down).
  - Add a floating "↓ [N] New Messages" button when the user is scrolled up and new messages arrive. Clicking it will jump to the bottom and fetch the latest messages (discarding intermediate history to save memory and processing, fetching latest `10 * pullHistorySize` as suggested).

## Verification Plan

### Automated Tests
- N/A for frontend UI logic, but backend DB query `LoadContextBefore` can be tested in `sqlite_store_test.go`.

### Manual Verification
1. Configure `initialHistorySize` to 5 and `pullHistorySize` to 10.
2. Open chat, verify only the 5 most recent messages are loaded.
3. Scroll to top, verify 10 more messages are loaded seamlessly without violent scroll jumps.
4. Verify "No more history" state when reaching the beginning of the chat.
5. While scrolled up examining history, trigger an LLM response or send a message from another client (if applicable), verify the "↓ New Message" floating button appears.
6. Click the floating button, verify UI jumps to the bottom and loads the latest contextual batch.
7. Monitor DOM node count in Chrome DevTools to ensure it does not exceed `~50` nodes (assuming `pullSize=10`) during extensive scrolling.
