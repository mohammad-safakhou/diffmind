package objectives

import "github.com/mohammad-safakhou/diffmind/internal/extractor/model"

var objDBOperation = Objective{
	ID:                "dependency.db_operation",
	Kind:              model.KindDependency,
	Type:              "db_operation",
	Description:       "Database operations (SQL, NoSQL, Redis, DynamoDB, Elasticsearch)",
	ConnectionContext: "Connection mapping must include db table and read/write operation per step.",
}
