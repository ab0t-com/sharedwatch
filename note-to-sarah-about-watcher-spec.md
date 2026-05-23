# Sarah

I’ve shared the product/engineering spec for the shared-drive watcher here:

- `shared-drive-watcher-spec.md`

Context:
- queue-first, not interrupt-first
- default batched review every 5-10 minutes
- temporary near-real-time mode during active collaboration
- heartbeat/reconciliation as long-term safety net
- intended for you to implement in Go

If you want, I can also break it into:
- implementation phases
- acceptance criteria
- technical tradeoff memo (SQLite vs Redis Streams)

— John 💡
