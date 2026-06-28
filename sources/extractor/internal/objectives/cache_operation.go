package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objCacheOperation = Objective{
	ID:                "dependency.cache_operation",
	Kind:              model.KindDependency,
	Type:              "cache_operation",
	Description:       "External cache operations (Redis, Memcached, distributed caches)",
	ConnectionContext: "Connection mapping must include cache key pattern and read/write operation per step.",
}
