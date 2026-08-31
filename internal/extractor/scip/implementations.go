package scip

import pb "github.com/scip-code/scip/bindings/go/scip"

func (i *Index) Implementations(symbol string) []string {
	if i == nil || symbol == "" {
		return nil
	}
	return i.implementationsBySymbol[symbol]
}

func (i *Index) SymbolInfo(symbol string) *pb.SymbolInformation {
	if i == nil {
		return nil
	}
	if info := i.symbolInfoBySymbol[symbol]; info != nil {
		return info
	}
	return i.externalInfoBySymbol[symbol]
}
