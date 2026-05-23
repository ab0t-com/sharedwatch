# STATE MODEL

## Runtime mode
- `passive`
- `active`

## Event lifecycle
- `pending`
- `processing`
- `processed`
- `failed`
- `suppressed`

## Digest lifecycle
- `pending`
- `read`
- `archived`

## Durable expectations
The system should survive restart without losing:
- queued events
- processed history
- current mode
- active TTL
- last consumer timestamp
- last reconcile timestamp
