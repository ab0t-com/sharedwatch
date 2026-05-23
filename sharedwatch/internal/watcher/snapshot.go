package watcher

import "sharedwatch/internal/catalog"

type FileState = catalog.FileState
type Snapshot = catalog.Snapshot

var BuildSnapshot = catalog.BuildSnapshot
var SnapshotHash = catalog.SnapshotHash
