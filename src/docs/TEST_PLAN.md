# TEST PLAN

## Core logic
- coalescing behavior for noisy modify bursts
- snapshot diff create/modify/delete detection
- active mode TTL expiry semantics
- digest summarization formatting

## Storage integration
- SQLite bootstrap
- event insert/claim/process path
- digest insert/read path
- runtime state persistence

## Manual validation once Go exists
1. `./install.sh`
2. `./.bin/sharedwatch status`
3. `./.bin/sharedwatch test emit hello.md`
4. `./.bin/sharedwatch consume`
5. `./.bin/sharedwatch digest list`
6. `./.bin/sharedwatch mode active --ttl 15m`
7. `./.bin/sharedwatch reconcile now`
