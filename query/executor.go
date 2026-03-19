package query

import (
	"fmt"
	"sort"
	"sync"

	"github.com/seankohjs/neo4g/graph"
	"github.com/seankohjs/neo4g/parser"
	"github.com/seankohjs/neo4g/storage"
	"golang.org/x/sync/errgroup"
)

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
	if params == nil {
		params = map[string]interface{}{}
	}

	// Resolve MATCH bindings
	var rows []Row
	if stmt.Match != nil {
		r, err := ex.resolveMatch(stmt.Match, params)
		if err != nil {
			return nil, err
		}
		rows = r
	}

	// Dispatch to the action clause
	switch {
	case stmt.Create != nil:
		return ex.execCreate(stmt.Create, rows, params)
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

// --- MATCH resolution ---

func (ex *Executor) resolveMatch(mc *parser.MatchClause, params map[string]interface{}) ([]Row, error) {
	// Resolve each pattern in parallel
	patternRows := make([][]Row, len(mc.Patterns))
	var g errgroup.Group
	var mu sync.Mutex

	for i, pat := range mc.Patterns {
		i, pat := i, pat
		g.Go(func() error {
			rows, err := ex.resolvePattern(pat)
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

	// Apply WHERE filter
	if mc.Where != nil {
		var filtered []Row
		for _, row := range rows {
			val, err := evalExpr(mc.Where, row, params)
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
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	var out []Row
	for _, ra := range a {
		for _, rb := range b {
			merged := make(Row, len(ra)+len(rb))
			for k, v := range ra {
				merged[k] = v
			}
			for k, v := range rb {
				merged[k] = v
			}
			out = append(out, merged)
		}
	}
	return out
}

// resolvePattern resolves a single pattern to rows.
func (ex *Executor) resolvePattern(pat *parser.Pattern) ([]Row, error) {
	elems := pat.Elements
	if len(elems) == 0 {
		return nil, fmt.Errorf("empty pattern")
	}

	// Single node pattern: (n:Label)
	first, ok := elems[0].(*parser.NodePattern)
	if !ok {
		return nil, fmt.Errorf("pattern must start with a node")
	}

	rows, err := ex.resolveNodePattern(first)
	if err != nil {
		return nil, err
	}

	// Walk edge-node pairs: -[e:TYPE]->(m)
	for i := 1; i+1 < len(elems); i += 2 {
		ep, ok := elems[i].(*parser.EdgePattern)
		if !ok {
			return nil, fmt.Errorf("expected edge at position %d", i)
		}
		np, ok := elems[i+1].(*parser.NodePattern)
		if !ok {
			return nil, fmt.Errorf("expected node at position %d", i+1)
		}
		rows, err = ex.expandEdge(rows, first, ep, np)
		if err != nil {
			return nil, err
		}
		first = np
	}

	return rows, nil
}

func (ex *Executor) resolveNodePattern(np *parser.NodePattern) ([]Row, error) {
	nodes, err := ex.scanner.ScanNodes(np.Labels)
	if err != nil {
		return nil, fmt.Errorf("scan nodes: %w", err)
	}

	// Filter by inline properties if present
	var rows []Row
	for _, n := range nodes {
		if np.Properties != nil && !matchProps(n.Properties, np.Properties) {
			continue
		}
		row := make(Row)
		if np.Variable != "" {
			row[np.Variable] = n
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// matchProps checks if a node/edge's properties satisfy the inline property map.
func matchProps(actual map[string]graph.Value, expected map[string]parser.Expr) bool {
	for key, expr := range expected {
		lit, ok := expr.(*parser.LiteralExpr)
		if !ok {
			continue
		}
		val, exists := actual[key]
		if !exists {
			return false
		}
		if ValueToGo(val) != lit.Value {
			return false
		}
	}
	return true
}

func (ex *Executor) expandEdge(rows []Row, srcNP *parser.NodePattern, ep *parser.EdgePattern, dstNP *parser.NodePattern) ([]Row, error) {
	dir := astDirToGraph(ep.Dir)
	edgeType := ""
	if len(ep.Types) > 0 {
		edgeType = ep.Types[0]
	}

	var out []Row
	for _, row := range rows {
		var srcNode *graph.Node
		if srcNP.Variable != "" {
			if n, ok := row[srcNP.Variable].(*graph.Node); ok {
				srcNode = n
			}
		}
		if srcNode == nil {
			continue
		}

		edges, err := ex.scanner.Expand(srcNode.ID, dir, edgeType)
		if err != nil {
			return nil, fmt.Errorf("expand: %w", err)
		}

		for _, edge := range edges {
			// Determine the "other" node ID
			otherID := edge.TargetID
			if ep.Dir == parser.DirLeft {
				otherID = edge.SourceID
			}

			// If dst has labels, verify
			if len(dstNP.Labels) > 0 {
				other, err := ex.store.GetNode(otherID)
				if err != nil {
					continue
				}
				if !hasLabels(other, dstNP.Labels) {
					continue
				}
				newRow := cloneRow(row)
				if ep.Variable != "" {
					newRow[ep.Variable] = edge
				}
				if dstNP.Variable != "" {
					newRow[dstNP.Variable] = other
				}
				out = append(out, newRow)
			} else {
				other, err := ex.store.GetNode(otherID)
				if err != nil {
					continue
				}
				newRow := cloneRow(row)
				if ep.Variable != "" {
					newRow[ep.Variable] = edge
				}
				if dstNP.Variable != "" {
					newRow[dstNP.Variable] = other
				}
				out = append(out, newRow)
			}
		}
	}

	return out, nil
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

func (ex *Executor) execCreate(cc *parser.CreateClause, matchRows []Row, params map[string]interface{}) (*Result, error) {
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
			created = append(created, NodeToMap(node))
		} else if len(pat.Elements) == 3 {
			// CREATE (a)-[:TYPE]->(b) — variables come from matchRows
			srcNP := pat.Elements[0].(*parser.NodePattern)
			ep := pat.Elements[1].(*parser.EdgePattern)
			dstNP := pat.Elements[2].(*parser.NodePattern)

			edgeType := ""
			if len(ep.Types) > 0 {
				edgeType = ep.Types[0]
			}

			eProps, err := evalPropMap(ep.Properties, nil, params)
			if err != nil {
				return nil, fmt.Errorf("CREATE edge props: %w", err)
			}

			if matchRows == nil {
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
				eid, err := ex.store.CreateEdge(edgeType, src, dst, eProps)
				if err != nil {
					return nil, fmt.Errorf("CreateEdge: %w", err)
				}
				edge, err := ex.store.GetEdge(eid)
				if err != nil {
					return nil, err
				}
				created = append(created, EdgeToMap(edge))
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
		Rows:    []RowData{{"count": count}},
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
		Rows:    []RowData{{"count": count}},
	}, nil
}
