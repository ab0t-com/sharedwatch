# Examples

## Typical flow

### Start service
```bash
./.bin/sharedwatch run
```

### Check status
```bash
./.bin/sharedwatch status
```

### Simulate a test event
```bash
./.bin/sharedwatch test emit hello-john.md
```

### Consume into a digest
```bash
./.bin/sharedwatch consume
```

### List digests
```bash
./.bin/sharedwatch digest list
```

### Show one digest
```bash
./.bin/sharedwatch digest show <id>
```

### Archive one digest
```bash
./.bin/sharedwatch digest archive <id>
```

## Operator note
This system is designed for pull-based review. It records changes quickly, but the human/assistant consumer checks them on an intentional cadence.
