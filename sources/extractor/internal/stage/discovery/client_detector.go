package discovery

import (
	"net/url"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/stage/discovery/clientspec"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// client_detector.go provides deterministic AST detection of connection-client
// definitions. It walks the AST against the declarative clientspec registry and
// emits model.ConnectionClient records for downstream instance resolution and
// operation-to-client linking. Instances come from a config anchor or, when a
// pattern reads an annotation attribute, from a literal endpoint.

// DetectClients runs the clientspec registry over the AST index and returns the
// connection clients it can ground.
func DetectClients(idx *astpkg.ProjectIndex) []model.ConnectionClient {
	if idx == nil {
		return nil
	}
	patterns := clientspec.Patterns()
	var out []model.ConnectionClient
	seen := map[string]bool{}
	for _, defs := range idx.Symbols {
		for _, s := range defs {
			for _, p := range patterns {
				if !patternMatches(idx, p, s) {
					continue
				}
				c := buildDetectedClient(p, s)
				key := c.Kind + "|" + strings.ToLower(c.Symbol)
				if c.Symbol == "" {
					key = c.Kind + "|" + strings.ToLower(c.LogicalName)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, c)
				break // one client per symbol
			}
		}
	}
	return out
}

func patternMatches(idx *astpkg.ProjectIndex, p clientspec.Pattern, s astpkg.SymbolDef) bool {
	if p.Annotation != "" && symbolHasAnnotation(s, p.Annotation) {
		return true
	}
	if p.SymbolSuffix != "" && isTypeSymbol(s) && strings.HasSuffix(s.Name, p.SymbolSuffix) {
		return true
	}
	if len(p.ImplementsAny) > 0 && symbolImplementsAny(idx, s, p.ImplementsAny) {
		return true
	}
	return false
}

func isTypeSymbol(s astpkg.SymbolDef) bool {
	return s.Kind == astpkg.SymbolKindClass || s.Kind == astpkg.SymbolKindInterface
}

func symbolHasAnnotation(s astpkg.SymbolDef, name string) bool {
	for _, a := range s.Annotations {
		if strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

func symbolImplementsAny(idx *astpkg.ProjectIndex, s astpkg.SymbolDef, ifaces []string) bool {
	match := func(impl string) bool {
		il := strings.ToLower(strings.TrimSpace(impl))
		return il == strings.ToLower(s.Qualified) ||
			il == strings.ToLower(s.Name) ||
			lastTypeSegment(il) == strings.ToLower(s.Name)
	}
	for _, iface := range ifaces {
		for _, impl := range idx.Implements[iface] {
			if match(impl) {
				return true
			}
		}
		for _, impl := range idx.Implements[lastTypeSegment(iface)] {
			if match(impl) {
				return true
			}
		}
	}
	return false
}

func buildDetectedClient(p clientspec.Pattern, s astpkg.SymbolDef) model.ConnectionClient {
	c := model.ConnectionClient{
		LogicalName: s.Name,
		Kind:        p.Kind,
		Symbol:      s.Qualified,
		Framework:   p.Framework,
		Source:      "ast",
		Locations:   []model.Location{{File: s.File, StartLine: int(s.Range.StartLine) + 1, EndLine: int(s.Range.EndLine) + 1}},
	}
	name := c.LogicalName
	if name == "" {
		name = c.Symbol
	}
	c.ID = util.StableID(string(model.KindClient), c.Kind, strings.ToLower(name), s.File)
	switch {
	case p.ConfigAnchorConst != "":
		c.ConfigAnchor = p.ConfigAnchorConst
	case p.ConfigAnchorAttr != "":
		if raw := symbolAnnotationAttr(s, p.Annotation, p.ConfigAnchorAttr); raw != "" {
			applyAttrInstance(&c, raw)
		}
	}
	return c
}

// symbolAnnotationAttr reads attr from the named annotation on the symbol.
func symbolAnnotationAttr(s astpkg.SymbolDef, annName, attr string) string {
	for _, a := range s.Annotations {
		if annName != "" && !strings.EqualFold(a.Name, annName) {
			continue
		}
		if v := annotationAttr(a.Arguments, attr); v != "" {
			return v
		}
	}
	return ""
}

// applyAttrInstance turns an annotation-attribute value into either a config
// anchor (a property key or ${placeholder} resolved later) or a literal
// InstanceRef (a hardcoded endpoint, harvested now).
func applyAttrInstance(c *model.ConnectionClient, raw string) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return
	case IsPlaceholder(raw):
		if body, _, ok := SplitPlaceholder(raw); ok && body != "" {
			c.ConfigAnchor = body
		}
	case strings.Contains(raw, "://") || strings.HasPrefix(strings.ToLower(raw), "http"):
		ref := &model.InstanceRef{Kind: "http", URLTemplate: raw, ResolvedURL: raw}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			ref.Host = u.Host
			ref.LogicalName = u.Host
		}
		if ref.LogicalName == "" {
			ref.LogicalName = c.LogicalName
		}
		c.InstanceRef = ref
	case looksLikePropertyKey(raw):
		c.ConfigAnchor = raw
	}
}

// MergeClients returns primary plus the secondary clients that don't duplicate a
// primary one. Identity is (kind + config anchor) first, then (kind + symbol-tail)
// and (kind + logical name), so client detectors converge on one client per real
// backbone instead of competing. Primary is authoritative; secondary fills gaps.
func MergeClients(primary, secondary []model.ConnectionClient) []model.ConnectionClient {
	index := map[string]bool{}
	for _, c := range primary {
		for _, k := range clientDedupKeys(c) {
			index[k] = true
		}
	}
	out := append([]model.ConnectionClient{}, primary...)
	for _, c := range secondary {
		dup := false
		for _, k := range clientDedupKeys(c) {
			if index[k] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, c)
		for _, k := range clientDedupKeys(c) {
			index[k] = true
		}
	}
	return out
}

func clientDedupKeys(c model.ConnectionClient) []string {
	var ks []string
	if a := strings.ToLower(strings.TrimSpace(c.ConfigAnchor)); a != "" {
		ks = append(ks, c.Kind+"|anchor:"+a)
	}
	if s := lastTypeSegment(strings.ToLower(strings.TrimSpace(c.Symbol))); s != "" {
		ks = append(ks, c.Kind+"|sym:"+s)
	}
	if n := strings.ToLower(strings.TrimSpace(c.LogicalName)); n != "" {
		ks = append(ks, c.Kind+"|name:"+n)
	}
	return ks
}
