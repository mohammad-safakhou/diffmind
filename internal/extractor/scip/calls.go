package scip

import "sort"

func (i *Index) FindOccurrencesBySymbol(symbol string) []CallSite {
	if i == nil || symbol == "" {
		return nil
	}
	var calls []CallSite
	for path, document := range i.documentsByPath {
		for _, occurrence := range document.GetOccurrences() {
			if occurrence.GetSymbol() != symbol {
				continue
			}
			calls = append(calls, CallSite{
				CalleeSymbol: symbol,
				At:           occurrenceToLocation(path, occurrence),
				Enclosing:    enclosingLocation(path, occurrence),
				Roles:        occurrence.GetSymbolRoles(),
			})
		}
	}
	sort.Slice(calls, func(a, b int) bool {
		if calls[a].At.File != calls[b].At.File {
			return calls[a].At.File < calls[b].At.File
		}
		if calls[a].At.StartLine != calls[b].At.StartLine {
			return calls[a].At.StartLine < calls[b].At.StartLine
		}
		return calls[a].At.StartCol < calls[b].At.StartCol
	})
	return calls
}

func (i *Index) CallsFrom(callerSymbol string) []CallSite {
	if i == nil || callerSymbol == "" {
		return nil
	}
	return i.callsBySymbol[callerSymbol]
}

func (i *Index) callsFromDefinition(definition symbolLocation) []CallSite {
	if i == nil {
		return nil
	}
	document := i.documentsByPath[definition.DocumentPath]
	if document == nil {
		return nil
	}
	occurrences := document.GetOccurrences()
	if definition.OccurrenceIndex < 0 || definition.OccurrenceIndex >= len(occurrences) {
		return nil
	}
	definitionOccurrence := occurrences[definition.OccurrenceIndex]
	callerSymbol := definitionOccurrence.GetSymbol()
	body := occurrenceEnclosing(definitionOccurrence)
	if body == nil {
		body = inferBodyRange(occurrences, definition.OccurrenceIndex)
		if body == nil {
			return nil
		}
	}

	var calls []CallSite
	for index, occurrence := range occurrences {
		if index == definition.OccurrenceIndex {
			continue
		}
		callee := occurrence.GetSymbol()
		if callee == "" || callee == callerSymbol || !isCallableSymbol(callee) {
			continue
		}
		roles := occurrence.GetSymbolRoles()
		if isDefinitionRole(roles) || !rangeContainedIn(occurrenceRange(occurrence), body) {
			continue
		}
		calls = append(calls, CallSite{
			CallerSymbol: callerSymbol,
			CalleeSymbol: callee,
			At:           occurrenceToLocation(definition.DocumentPath, occurrence),
			Enclosing:    enclosingLocation(definition.DocumentPath, occurrence),
			Roles:        roles,
		})
	}
	sortCallSites(calls)
	return calls
}

func sortCallSites(calls []CallSite) {
	sort.Slice(calls, func(a, b int) bool {
		if calls[a].At.File != calls[b].At.File {
			return calls[a].At.File < calls[b].At.File
		}
		if calls[a].At.StartLine != calls[b].At.StartLine {
			return calls[a].At.StartLine < calls[b].At.StartLine
		}
		if calls[a].At.StartCol != calls[b].At.StartCol {
			return calls[a].At.StartCol < calls[b].At.StartCol
		}
		return calls[a].CalleeSymbol < calls[b].CalleeSymbol
	})
}
