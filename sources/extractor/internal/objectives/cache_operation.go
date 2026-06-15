package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objCacheOperation = Objective{
	ID:          "dependency.cache_operation",
	Kind:        model.KindDependency,
	Type:        "cache_operation",
	Description: "External cache operations (Redis, Memcached, distributed caches)",
	DiscoveryPrompt: `Find ALL external/distributed cache operations in this service.

PATTERNS TO CHECK:
- Redis: RedisTemplate, StringRedisTemplate, Jedis, JedisPool, Lettuce, RedisService
- Spring Cache with Redis backing: @Cacheable, @CacheEvict, @CachePut with Redis CacheManager
- Memcached: MemcachedClient
- Hazelcast: HazelcastInstance
- Python: redis-py, aioredis

FOR EACH CACHE OPERATION EXTRACT:
- Cache type (Redis, Memcached, etc.)
- Operation type (get/set/delete/expire)
- Key pattern/prefix
- Cache name or namespace
- TTL/expiration settings
- Service class that uses the cache

NOTE: In-memory-only caches (EhCache without external store, Caffeine, Guava Cache) are NOT external cache operations - do not include them.

BOUNDARY (Redis ownership): Redis used as a CACHE (TTL'd keys, @Cacheable,
cache-aside) is a cache_operation and belongs HERE. Redis used as a PRIMARY
DATASTORE (durable keys, no TTL, source of truth) is a db_operation. Pick ONE;
do not report the same Redis access as both.
If no external cache operations exist, return {"items": []}.`,
	ConnectionContext: "Connection mapping must include cache key pattern and read/write operation per step.",
}
