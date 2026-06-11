package connections

import (
	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/repositorycall"
)

// Repository operation policy is exported for deterministic discovery, which
// shares the connection stage's precision rules when projecting AST calls.
func IsRepositoryOperationSymbol(symbol string) bool {
	return repositorycall.IsOperationSymbol(symbol)
}

func NormalizeRepositoryOperationName(symbol string) string {
	return repositorycall.NormalizeOperationName(symbol)
}

func SplitOwnerMethod(name string) (string, string, bool) {
	return repositorycall.SplitOwnerMethod(name)
}

func TableEntityFromRepository(owner string) (string, string) {
	return repositorycall.TableEntity(owner)
}

func IsLowSignalRepositoryOwner(owner string) bool {
	return repositorycall.IsLowSignalOwner(owner)
}

func IsJunkTableName(table string) bool {
	return repositorycall.IsJunkTable(table)
}

func InferDBOperationKind(index *astpkg.ProjectIndex, symbol string) string {
	return repositorycall.InferOperationKind(index, symbol)
}
