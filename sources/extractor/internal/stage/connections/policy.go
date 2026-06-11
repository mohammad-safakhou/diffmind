package connections

import astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"

// Repository operation policy is exported for deterministic discovery, which
// shares the connection stage's precision rules when projecting AST calls.
func IsRepositoryOperationSymbol(symbol string) bool {
	return isRepositoryOperationSymbol(symbol)
}

func NormalizeRepositoryOperationName(symbol string) string {
	return normalizeRepositoryOperationName(symbol)
}

func SplitOwnerMethod(name string) (string, string, bool) {
	return splitOwnerMethod(name)
}

func TableEntityFromRepository(owner string) (string, string) {
	return tableEntityFromRepository(owner)
}

func IsLowSignalRepositoryOwner(owner string) bool {
	return isLowSignalRepositoryOwner(owner)
}

func IsJunkTableName(table string) bool {
	return isJunkTableName(table)
}

func InferDBOperationKind(index *astpkg.ProjectIndex, symbol string) string {
	return inferDBOperationKindAST(index, symbol)
}
