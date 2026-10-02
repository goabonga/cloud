// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state

import (
	"context"
	"strings"
)

// etcdScheme prefixes a DSN that selects the etcd backend, e.g.
// "etcd://10.0.0.1:2379,10.0.0.2:2379".
const etcdScheme = "etcd://"

// Open returns the configured store, selected by dsn:
//   - "etcd://ep1,ep2,..." -> an etcd-backed store (HA, multi-instance);
//   - any other non-empty dsn -> a PostgreSQL-backed store (HA, multi-instance);
//   - empty dsn -> a file store rooted at dir (single host).
func Open(dir, dsn string) (Store, error) {
	switch {
	case strings.HasPrefix(dsn, etcdScheme):
		endpoints := strings.Split(strings.TrimPrefix(dsn, etcdScheme), ",")
		return NewEtcdStore(endpoints)
	case dsn != "":
		return NewPostgresStore(context.Background(), dsn)
	default:
		return NewFileStore(dir), nil
	}
}
