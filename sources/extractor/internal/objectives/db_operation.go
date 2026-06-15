package objectives

import "github.com/mohammad-safakhou/diffmind/internal/model"

var objDBOperation = Objective{
	ID:          "dependency.db_operation",
	Kind:        model.KindDependency,
	Type:        "db_operation",
	Description: "Database operations (SQL, NoSQL, Redis, DynamoDB, Elasticsearch)",
	DiscoveryPrompt: `Find ALL database operations reachable from this service's code.

FRAMEWORK-SPECIFIC PATTERNS TO CHECK:
- Spring Data JPA: @Repository, JpaRepository, CrudRepository, @Query, @Modifying, EntityManager
- Spring JDBC: JdbcTemplate, NamedParameterJdbcTemplate
- MyBatis: @Mapper, @Select, @Insert, @Update, @Delete, XML mapper files
- Hibernate: Session, SessionFactory, @Entity classes
- Redis: RedisTemplate, StringRedisTemplate, Jedis, JedisPool, RedisService, @Cacheable/@CacheEvict/@CachePut with Redis cache manager, Lettuce
- DynamoDB: DynamoDBMapper, DynamoDBTable, PynamoDB (Python), @DynamoDBTable
- Elasticsearch: ElasticsearchRepository, RestHighLevelClient, ElasticsearchOperations
- MongoDB: MongoRepository, MongoTemplate
- Raw SQL: DataSource, Connection, PreparedStatement
- Python: psycopg2, SQLAlchemy, boto3 dynamodb, redis-py
- Liquibase/Flyway migrations (mention but don't list as runtime ops)

FOR EACH DB OPERATION EXTRACT:
- Database type (PostgreSQL, MySQL, Redis, DynamoDB, Elasticsearch, MongoDB)
- Table/entity/key name
- Operation type (read/write/upsert/delete)
- Repository/DAO class and method name
- The datasource/connection config property name

IMPORTANT:
- Check application.yml/properties for datasource configuration to identify database type and connection details
- Check any *values.yaml / config/*.yaml for database connection environment variables
- Report the HIGH-LEVEL data dependency, not a per-method inventory: emit ONE
  item per distinct (table/entity, operation-type) pair. Five different SELECT
  methods on the "orders" table are ONE read item; a read and a write on the
  same table are two items. Always populate details.table and details.operation
  (read/write/upsert/delete) so duplicates collapse cleanly.
- Redis GET/SET/DEL operations count as db_operations
- In-memory caches (EhCache, Caffeine) with NO external backing store are NOT db_operations`,
	ConnectionContext: "Connection mapping must include db table and read/write operation per step.",
}
