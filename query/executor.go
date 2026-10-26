package query

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/parser"
	"github.com/neo4g/neo4g/storage"
	"golang.org/x/sync/errgroup"
)

// currentNodeKey holds the node a pattern is expanding from when that node
// has no variable. The leading NUL keeps it out of user-visible column names.
const currentNodeKey = "\x00node"

// Executor evaluates parsed Cypher-lite statements against the storage engine.
type Executor struct {
	store   storage.Store
	scanner storage.Scanner
}

// NewExecutor creates an Executor backed by the given store and scanner.
func NewExecutor(store storage.Store, scanner storage.Scanner) *Executor {
	return &Executor{store: store, scanner: scanner}
}

// Execute runs a parsed statement with the given parameters and returns a Result.
func (ex *Executor) Execute(stmt *parser.Statement, params map[string]interface{}) (*Result, error) {
	params = normalizeParams(params)

	// Resolve MATCH bindings. An empty slice means the clause matched nothing,
	// which is different from a statement with no MATCH clause.
	var rows []Row
	hasMatch := stmt.Match != nil
	if hasMatch {
		r, err := ex.resolveMatch(stmt.Match, params)
		if err != nil {
			return nil, err
		}
		rows = r
	}

	// Dispatch to the action clause
	switch {
	case stmt.Create != nil:
		return ex.execCreate(stmt.Create, rows, params, hasMatch)
	case stmt.Return != nil:
		return ex.execReturn(stmt.Return, rows, params)
	case stmt.Set != nil:
		return ex.execSet(stmt.Set, rows, params)
	case stmt.Delete != nil:
		return ex.execDelete(stmt.Delete, rows)
	default:
		return nil, fmt.Errorf("statement has no action clause")
	}
}

// --- Predicate pushdown ---

type propFilter struct {
	key string
	val interface{}
}

// extractPredicates decomposes a WHERE expression into per-variable property
// equality filters that can be pushed down into ScanNodes, lone id(var) = const
// lookups for anchor variables, and a residual expression evaluated after the join.
// badIDs contains anchors constrained to two different ids; those matches are empty.
func extractPredicates(
	where parser.Expr,
	params map[string]interface{},
	patternVars map[string]bool,
) (map[string][]propFilter, map[string]graph.NodeID, map[string]bool, parser.Expr) {
	if where == nil {
		return nil, nil, nil, nil
	}
	filters := make(map[string][]propFilter)
	ids := make(map[string]graph.NodeID)
	badIDs := make(map[string]bool)
	residual := extractRec(where, params, filters, ids, badIDs, patternVars)
	if len(filters) == 0 {
		filters = nil
	}
	if len(ids) == 0 {
		ids = nil
	}
	if len(badIDs) == 0 {
		badIDs = nil
	}
	return filters, ids, badIDs, residual
}

func extractRec(
	e parser.Expr,
	params map[string]interface{},
	out map[string][]propFilter,
	ids map[string]graph.NodeID,
	badIDs map[string]bool,
	vars map[string]bool,
) parser.Expr {
	bin, ok := e.(*parser.BinaryExpr)
	if !ok {
		return e
	}

	if bin.Op == parser.AND {
		left := extractRec(bin.Left, params, out, ids, badIDs, vars)
		right := extractRec(bin.Right, params, out, ids, badIDs, vars)
		if left == nil && right == nil {
			return nil
		}
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		return &parser.BinaryExpr{Op: parser.AND, Left: left, Right: right}
	}

	if bin.Op != parser.EQ {
		return e
	}

	prop, val := matchEqPredicate(bin, params)
	if prop != nil && vars[prop.Variable] {
		out[prop.Variable] = append(out[prop.Variable], propFilter{key: prop.Key, val: val})
		return nil
	}

	if name, id, ok := matchIDPredicate(bin, params); ok && vars[name] {
		if prev, exists := ids[name]; exists && prev != id {
			badIDs[name] = true
		} else {
			ids[name] = id
		}
		return nil
	}

	return e
}

func matchEqPredicate(bin *parser.BinaryExpr, params map[string]interface{}) (*parser.PropExpr, interface{}) {
	// prop = literal | literal = prop | prop = $param | $param = prop
	if prop, ok := bin.Left.(*parser.PropExpr); ok {
		if v := constValue(bin.Right, params); v != nil {
			return prop, v
		}
	}
	if prop, ok := bin.Right.(*parser.PropExpr); ok {
		if v := constValue(bin.Left, params); v != nil {
			return prop, v
		}
	}
	return nil, nil
}

func constValue(e parser.Expr, params map[string]interface{}) interface{} {
	switch ex := e.(type) {
	case *parser.LiteralExpr:
		return normalizeScalar(ex.Value)
	case *parser.ParamExpr:
		if v, ok := params[ex.Name]; ok {
			return normalizeScalar(v)
		}
	}
	return nil
}

// matchIDPredicate matches id(var) = const or const = id(var).
func matchIDPredicate(bin *parser.BinaryExpr, params map[string]interface{}) (string, graph.NodeID, bool) {
	if name, id, ok := idConst(bin.Left, bin.Right, params); ok {
		return name, id, true
	}
	return idConst(bin.Right, bin.Left, params)
}

func idConst(fn, val parser.Expr, params map[string]interface{}) (string, graph.NodeID, bool) {
	f, ok := fn.(*parser.FuncExpr)
	if !ok || strings.ToLower(f.Name) != "id" || len(f.Args) != 1 {
		return "", 0, false
	}
	ident, ok := f.Args[0].(*parser.IdentExpr)
	if !ok || ident.Name == "" {
		return "", 0, false
	}
	id, ok := asNodeID(constValue(val, params))
	if !ok {
		return "", 0, false
	}
	return ident.Name, id, true
}

func asNodeID(v interface{}) (graph.NodeID, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		if n < 0 {
			return 0, false
		}
		return graph.NodeID(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return graph.NodeID(n), true
	case float64:
		i, ok := integralInt64(n)
		if !ok || i < 0 {
			return 0, false
		}
		return graph.NodeID(i), true
	default:
		return 0, false
	}
}

func matchFilters(node *graph.Node, filters []propFilter) bool {
	for _, f := range filters {
		v, ok := node.Properties[f.key]
		if !ok {
			return false
		}
		if !valuesEqual(ValueToGo(v), f.val) {
			return false
		}
	}
	return true
}

// valuesEqual uses the same comparison as WHERE, including numeric promotion.
// Incomparable types are not equal.
func valuesEqual(left, right interface{}) bool {
	eq, err := compare(parser.EQ, left, right)
	if err != nil {
		return false
	}
	b, ok := eq.(bool)
	return ok && b
}

// --- MATCH resolution ---

func (ex *Executor) resolveMatch(mc *parser.MatchClause, params map[string]interface{}) ([]Row, error) {
	// Collect known pattern variables for safe extraction
	patVars := make(map[string]bool)
	for _, pat := range mc.Patterns {
		if len(pat.Elements) > 0 {
			if np, ok := pat.Elements[0].(*parser.NodePattern); ok && np.Variable != "" {
				patVars[np.Variable] = true
			}
		}
	}

	// Extract per-variable pushdown filters and anchor id lookups from WHERE
	pushdown, idLookup, badIDs, residual := extractPredicates(mc.Where, params, patVars)

	// Resolve each pattern in parallel, passing pushdown filters
	patternRows := make([][]Row, len(mc.Patterns))
	var g errgroup.Group
	var mu sync.Mutex

	for i, pat := range mc.Patterns {
		g.Go(func() error {
			rows, err := ex.resolvePatternWithFilters(pat, pushdown, idLookup, badIDs, params)
			if err != nil {
				return err
			}
			mu.Lock()
			patternRows[i] = rows
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Cross-product all pattern results
	rows := patternRows[0]
	for i := 1; i < len(patternRows); i++ {
		rows = crossProduct(rows, patternRows[i])
	}

	// Apply residual WHERE filter (predicates that couldn't be pushed down)
	if residual != nil {
		var filtered []Row
		for _, row := range rows {
			val, err := evalExpr(residual, row, params)
			if err != nil {
				return nil, fmt.Errorf("WHERE: %w", err)
			}
			if b, ok := val.(bool); ok && b {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}

	return rows, nil
}

func crossProduct(a, b []Row) []Row {
	// The product of an empty side is empty. Returning the other side makes
	// MATCH (a:Missing), (b:Person) DELETE b delete every person.
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	var out []Row
	for _, ra := range a {
		for _, rb := range b {
			merged, ok := mergeRows(ra, rb)
			if !ok {
				continue
			}
			out = append(out, merged)
		}
	}
	return out
}

// mergeRows joins two pattern rows. A name bound on both sides is an identity
// constraint: the row is kept only when both bindings are the same node or edge.
func mergeRows(a, b Row) (Row, bool) {
	merged := make(Row, len(a)+len(b))
	for k, v := range a {
		if internalKey(k) {
			continue
		}
		merged[k] = v
	}
	for k, v := range b {
		if internalKey(k) {
			continue
		}
		if existing, ok := merged[k]; ok {
			if !sameEntity(existing, v) {
				return nil, false
			}
			continue
		}
		merged[k] = v
	}
	return merged, true
}

func sameEntity(a, b interface{}) bool {
	switch av := a.(type) {
	case *graph.Node:
		bv, ok := b.(*graph.Node)
		return ok && av.ID == bv.ID
	case *graph.Edge:
		bv, ok := b.(*graph.Edge)
		return ok && av.ID == bv.ID
	default:
		return false
	}
}

// resolvePatternWithFilters resolves a single pattern to rows, applying any
// pushed-down property filters to the first node pattern.
func (ex *Executor) resolvePatternWithFilters(
	pat *parser.Pattern,
	pushdown map[string][]propFilter,
	idLookup map[string]graph.NodeID,
	badIDs map[string]bool,
	params map[string]interface{},
) ([]Row, error) {
	elems := pat.Elements
	if len(elems) == 0 {
		return nil, fmt.Errorf("empty pattern")
	}

	first, ok := elems[0].(*parser.NodePattern)
	if !ok {
		return nil, fmt.Errorf("pattern must start with a node")
	}

	if first.Variable != "" && badIDs[first.Variable] {
		return nil, nil
	}

	var filters []propFilter
	if pushdown != nil && first.Variable != "" {
		filters = pushdown[first.Variable]
	}

	var idPtr *graph.NodeID
	if first.Variable != "" && idLookup != nil {
		if id, ok := idLookup[first.Variable]; ok {
			idCopy := id
			idPtr = &idCopy
		}
	}

	rows, err := ex.resolveNodePattern(first, filters, idPtr, params)
	if err != nil {
		return nil, err
	}

	for i := 1; i+1 < len(elems); i += 2 {
		ep, ok := elems[i].(*parser.EdgePattern)
		if !ok {
			return nil, fmt.Errorf("expected edge at position %d", i)
		}
		np, ok := elems[i+1].(*parser.NodePattern)
		if !ok {
			return nil, fmt.Errorf("expected node at position %d", i+1)
		}
		rows, err = ex.expandEdge(rows, first, ep, np, params)
		if err != nil {
			return nil, err
		}
		first = np
	}

	stripInternal(rows)
	return rows, nil
}

func (ex *Executor) resolveNodePattern(np *parser.NodePattern, filters []propFilter, idLookup *graph.NodeID, params map[string]interface{}) ([]Row, error) {
	var nodes []*graph.Node
	if idLookup != nil {
		node, err := ex.store.GetNode(*idLookup)
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("get node %d: %w", *idLookup, err)
		}
		nodes = []*graph.Node{node}
	} else {
		var err error
		nodes, err = ex.scanner.ScanNodes(np.Labels)
		if err != nil {
			return nil, fmt.Errorf("scan nodes: %w", err)
		}
	}

	var rows []Row
	for _, n := range nodes {
		// ScanNodes keeps a node when any requested label matches. :A:B means all of them.
		if len(np.Labels) > 0 && !hasLabels(n, np.Labels) {
			continue
		}
		if len(np.Properties) > 0 {
			ok, err := propsMatch(n.Properties, np.Properties, Row{}, params)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if len(filters) > 0 && !matchFilters(n, filters) {
			continue
		}
		row := make(Row)
		bindNode(row, np.Variable, n)
		rows = append(rows, row)
	}

	return rows, nil
}

// propsMatch reports whether actual properties equal every expression in the
// pattern map. Equality is the same comparison WHERE uses, so parameters and
// numeric types count. A missing property does not match a non-null value.
func propsMatch(actual map[string]graph.Value, expected map[string]parser.Expr, row Row, params map[string]interface{}) (bool, error) {
	for key, expr := range expected {
		want, err := evalExpr(expr, row, params)
		if err != nil {
			return false, fmt.Errorf("property %s: %w", key, err)
		}
		var got interface{}
		if actual != nil {
			if val, exists := actual[key]; exists {
				got = ValueToGo(val)
			}
		}
		if !valuesEqual(got, want) {
			return false, nil
		}
	}
	return true, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

func (ex *Executor) expandEdge(
	rows []Row,
	srcNP *parser.NodePattern,
	ep *parser.EdgePattern,
	dstNP *parser.NodePattern,
	params map[string]interface{},
) ([]Row, error) {
	dir := astDirToGraph(ep.Dir)
	edgeType := ""
	if len(ep.Types) > 0 {
		edgeType = ep.Types[0]
	}

	var out []Row
	for _, row := range rows {
		srcNode := boundNode(row, srcNP.Variable)
		if srcNode == nil {
			return nil, fmt.Errorf("expand: source node is not bound")
		}

		edges, err := ex.scanner.Expand(srcNode.ID, dir, edgeType)
		if err != nil {
			return nil, fmt.Errorf("expand: %w", err)
		}

		for _, edge := range edges {
			newRow, ok, err := ex.expandOne(row, srcNode.ID, edge, ep, dstNP, params)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, newRow)
			}
		}
	}

	return out, nil
}

// expandOne reports whether edge satisfies the pattern from this row and, when
// it does, the row extended with that edge and its other endpoint.
func (ex *Executor) expandOne(
	row Row,
	srcID graph.NodeID,
	edge *graph.Edge,
	ep *parser.EdgePattern,
	dstNP *parser.NodePattern,
	params map[string]interface{},
) (Row, bool, error) {
	if ep.Variable != "" {
		if existing, ok := row[ep.Variable]; ok && !sameEntity(existing, edge) {
			return nil, false, nil
		}
	}
	if len(ep.Properties) > 0 {
		ok, err := propsMatch(edge.Properties, ep.Properties, row, params)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	// The neighbor is the endpoint that is not the node being expanded.
	// Incoming edges from Expand(..., Both) have TargetID == src.
	otherID := otherEndpoint(edge, srcID)

	var dstNode *graph.Node
	if dstNP.Variable != "" {
		if existing, ok := row[dstNP.Variable]; ok {
			n, isNode := existing.(*graph.Node)
			if !isNode || n.ID != otherID {
				return nil, false, nil
			}
			dstNode = n
		}
	}

	needNode := dstNode == nil && (dstNP.Variable != "" || len(dstNP.Labels) > 0 || len(dstNP.Properties) > 0)
	if needNode {
		fetched, err := ex.store.GetNode(otherID)
		if err != nil {
			return nil, false, fmt.Errorf("get node %d: %w", otherID, err)
		}
		if fetched == nil {
			return nil, false, fmt.Errorf("get node %d: empty result", otherID)
		}
		dstNode = fetched
	}

	if len(dstNP.Labels) > 0 && (dstNode == nil || !hasLabels(dstNode, dstNP.Labels)) {
		return nil, false, nil
	}
	if len(dstNP.Properties) > 0 {
		var props map[string]graph.Value
		if dstNode != nil {
			props = dstNode.Properties
		}
		ok, err := propsMatch(props, dstNP.Properties, row, params)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
	}

	return rowWithExpansion(row, edge, ep, dstNP, dstNode, otherID), true, nil
}

func rowWithExpansion(
	row Row,
	edge *graph.Edge,
	ep *parser.EdgePattern,
	dstNP *parser.NodePattern,
	dstNode *graph.Node,
	otherID graph.NodeID,
) Row {
	newRow := cloneRow(row)
	if ep.Variable != "" {
		if _, bound := row[ep.Variable]; !bound {
			newRow[ep.Variable] = edge
		}
	}
	if dstNode == nil {
		dstNode = &graph.Node{ID: otherID}
	}
	if dstNP.Variable != "" {
		if _, bound := row[dstNP.Variable]; !bound {
			newRow[dstNP.Variable] = dstNode
		}
	}
	newRow[currentNodeKey] = dstNode
	return newRow
}

func otherEndpoint(edge *graph.Edge, src graph.NodeID) graph.NodeID {
	if edge.SourceID == src {
		return edge.TargetID
	}
	return edge.SourceID
}

func boundNode(row Row, variable string) *graph.Node {
	if variable != "" {
		n, _ := row[variable].(*graph.Node)
		return n
	}
	n, _ := row[currentNodeKey].(*graph.Node)
	return n
}

func bindNode(row Row, variable string, n *graph.Node) {
	row[currentNodeKey] = n
	if variable != "" {
		row[variable] = n
	}
}

func stripInternal(rows []Row) {
	for _, row := range rows {
		delete(row, currentNodeKey)
	}
}

func internalKey(k string) bool {
	return len(k) > 0 && k[0] == 0
}

func hasLabels(n *graph.Node, labels []string) bool {
	set := make(map[string]bool, len(n.Labels))
	for _, l := range n.Labels {
		set[l] = true
	}
	for _, l := range labels {
		if !set[l] {
			return false
		}
	}
	return true
}

func astDirToGraph(d parser.Direction) graph.Direction {
	switch d {
	case parser.DirRight:
		return graph.Outgoing
	case parser.DirLeft:
		return graph.Incoming
	default:
		return graph.Both
	}
}

func cloneRow(r Row) Row {
	c := make(Row, len(r))
	for k, v := range r {
		c[k] = v
	}
	return c
}

// --- CREATE ---

func (ex *Executor) execCreate(cc *parser.CreateClause, matchRows []Row, params map[string]interface{}, hasMatch bool) (*Result, error) {
	var created []RowData

	for _, pat := range cc.Patterns {
		if len(pat.Elements) == 1 {
			// CREATE (n:Label {props})
			np, ok := pat.Elements[0].(*parser.NodePattern)
			if !ok {
				return nil, fmt.Errorf("CREATE pattern must start with a node")
			}
			props, err := evalPropMap(np.Properties, nil, params)
			if err != nil {
				return nil, fmt.Errorf("CREATE node props: %w", err)
			}
			nid, err := ex.store.CreateNode(np.Labels, props)
			if err != nil {
				return nil, fmt.Errorf("CreateNode: %w", err)
			}
			node, err := ex.store.GetNode(nid)
			if err != nil {
				return nil, err
			}
			created = append(created, RowData{"created": NodeToMap(node)})
		} else if len(pat.Elements) == 3 {
			// CREATE (a)-[:TYPE]->(b) between nodes already bound by MATCH.
			// Creating missing endpoints is intentionally not done here: each
			// Store call auto-commits, so a failure after the first CreateNode
			// would leave a partial pattern. Unbound endpoints still error,
			// and the graph is left unchanged.
			srcNP := pat.Elements[0].(*parser.NodePattern)
			ep := pat.Elements[1].(*parser.EdgePattern)
			dstNP := pat.Elements[2].(*parser.NodePattern)

			edgeType := ""
			if len(ep.Types) > 0 {
				edgeType = ep.Types[0]
			}

			if !hasMatch {
				return nil, fmt.Errorf("CREATE edge requires MATCH to bind variables")
			}

			for _, row := range matchRows {
				src, err := resolveNodeID(row, srcNP.Variable)
				if err != nil {
					return nil, err
				}
				dst, err := resolveNodeID(row, dstNP.Variable)
				if err != nil {
					return nil, err
				}
				eProps, err := evalPropMap(ep.Properties, row, params)
				if err != nil {
					return nil, fmt.Errorf("CREATE edge props: %w", err)
				}
				if ep.Dir == parser.DirLeft {
					src, dst = dst, src
				}
				eid, err := ex.store.CreateEdge(edgeType, src, dst, eProps)
				if err != nil {
					return nil, fmt.Errorf("CreateEdge: %w", err)
				}
				edge, err := ex.store.GetEdge(eid)
				if err != nil {
					return nil, err
				}
				created = append(created, RowData{"created": EdgeToMap(edge)})
			}
		} else {
			return nil, fmt.Errorf("unsupported CREATE pattern with %d elements", len(pat.Elements))
		}
	}

	return &Result{
		Columns: []string{"created"},
		Rows:    created,
	}, nil
}

func resolveNodeID(row Row, variable string) (graph.NodeID, error) {
	v, ok := row[variable]
	if !ok {
		return 0, fmt.Errorf("unbound variable %q in CREATE", variable)
	}
	n, ok := v.(*graph.Node)
	if !ok {
		return 0, fmt.Errorf("variable %q is not a node", variable)
	}
	return n.ID, nil
}

func evalPropMap(props map[string]parser.Expr, row Row, params map[string]interface{}) (map[string]graph.Value, error) {
	if props == nil {
		return nil, nil
	}
	result := make(map[string]graph.Value, len(props))
	for k, expr := range props {
		val, err := evalExpr(expr, row, params)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", k, err)
		}
		result[k] = GoToValue(val)
	}
	return result, nil
}

// --- RETURN ---

func (ex *Executor) execReturn(rc *parser.ReturnClause, rows []Row, params map[string]interface{}) (*Result, error) {
	if rc.Star {
		return ex.execReturnStar(rows)
	}

	columns := make([]string, len(rc.Items))
	for i, item := range rc.Items {
		if item.Alias != "" {
			columns[i] = item.Alias
		} else {
			columns[i] = exprName(item.Expr)
		}
	}

	var resultRows []RowData
	for _, row := range rows {
		rd := make(RowData, len(rc.Items))
		for i, item := range rc.Items {
			val, err := evalExpr(item.Expr, row, params)
			if err != nil {
				return nil, fmt.Errorf("RETURN: %w", err)
			}
			rd[columns[i]] = serialize(val)
		}
		resultRows = append(resultRows, rd)
	}

	return &Result{Columns: columns, Rows: resultRows}, nil
}

func (ex *Executor) execReturnStar(rows []Row) (*Result, error) {
	if len(rows) == 0 {
		return &Result{}, nil
	}

	// Collect column names from first row, sorted for determinism
	colSet := make(map[string]bool)
	for k := range rows[0] {
		if internalKey(k) {
			continue
		}
		colSet[k] = true
	}
	columns := make([]string, 0, len(colSet))
	for k := range colSet {
		columns = append(columns, k)
	}
	sort.Strings(columns)

	var resultRows []RowData
	for _, row := range rows {
		rd := make(RowData, len(columns))
		for _, col := range columns {
			rd[col] = serialize(row[col])
		}
		resultRows = append(resultRows, rd)
	}

	return &Result{Columns: columns, Rows: resultRows}, nil
}

// exprName generates a default column name from an expression.
func exprName(e parser.Expr) string {
	switch ex := e.(type) {
	case *parser.IdentExpr:
		return ex.Name
	case *parser.PropExpr:
		return ex.Variable + "." + ex.Key
	case *parser.FuncExpr:
		return ex.Name + "(...)"
	default:
		return "expr"
	}
}

// serialize converts a Row value to a JSON-friendly representation.
func serialize(v interface{}) interface{} {
	switch obj := v.(type) {
	case *graph.Node:
		return NodeToMap(obj)
	case *graph.Edge:
		return EdgeToMap(obj)
	default:
		return v
	}
}

// --- SET ---

func (ex *Executor) execSet(sc *parser.SetClause, rows []Row, params map[string]interface{}) (*Result, error) {
	count := 0
	for _, row := range rows {
		// Collect updates per variable
		updates := make(map[string]map[string]graph.Value)
		for _, item := range sc.Items {
			val, err := evalExpr(item.Value, row, params)
			if err != nil {
				return nil, fmt.Errorf("SET %s.%s: %w", item.Variable, item.Property, err)
			}
			if updates[item.Variable] == nil {
				updates[item.Variable] = make(map[string]graph.Value)
			}
			updates[item.Variable][item.Property] = GoToValue(val)
		}

		for varName, props := range updates {
			v, ok := row[varName]
			if !ok {
				return nil, fmt.Errorf("SET: unbound variable %q", varName)
			}
			n, ok := v.(*graph.Node)
			if !ok {
				return nil, fmt.Errorf("SET: variable %q is not a node", varName)
			}
			// Merge with existing properties
			merged := make(map[string]graph.Value, len(n.Properties)+len(props))
			for k, v := range n.Properties {
				merged[k] = v
			}
			for k, v := range props {
				merged[k] = v
			}
			if err := ex.store.UpdateNode(n.ID, merged); err != nil {
				return nil, fmt.Errorf("UpdateNode: %w", err)
			}
			count++
		}
	}

	return &Result{
		Columns: []string{"updated"},
		Rows:    []RowData{{"updated": count}},
	}, nil
}

// --- DELETE ---

func (ex *Executor) execDelete(dc *parser.DeleteClause, rows []Row) (*Result, error) {
	count := 0
	for _, row := range rows {
		for _, varName := range dc.Variables {
			v, ok := row[varName]
			if !ok {
				return nil, fmt.Errorf("DELETE: unbound variable %q", varName)
			}
			switch obj := v.(type) {
			case *graph.Node:
				if err := ex.store.DeleteNode(obj.ID); err != nil {
					return nil, fmt.Errorf("DeleteNode: %w", err)
				}
				count++
			case *graph.Edge:
				if err := ex.store.DeleteEdge(obj.ID); err != nil {
					return nil, fmt.Errorf("DeleteEdge: %w", err)
				}
				count++
			default:
				return nil, fmt.Errorf("DELETE: variable %q is not a node or edge", varName)
			}
		}
	}

	return &Result{
		Columns: []string{"deleted"},
		Rows:    []RowData{{"deleted": count}},
	}, nil
}
