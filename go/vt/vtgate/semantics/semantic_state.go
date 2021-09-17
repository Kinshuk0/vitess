/*
Copyright 2020 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package semantics

import (
	"strings"

	"vitess.io/vitess/go/vt/key"
	querypb "vitess.io/vitess/go/vt/proto/query"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	vtrpcpb "vitess.io/vitess/go/vt/proto/vtrpc"
	"vitess.io/vitess/go/vt/vterrors"
	"vitess.io/vitess/go/vt/vtgate/engine"
	"vitess.io/vitess/go/vt/vtgate/vindexes"

	"vitess.io/vitess/go/vt/sqlparser"
)

type (
	// TableInfo contains information about tables
	TableInfo interface {
		Matches(name sqlparser.TableName) bool
		Authoritative() bool
		Name() (sqlparser.TableName, error)
		GetExpr() *sqlparser.AliasedTableExpr
		GetVindexTable() *vindexes.Table
		GetColumns() []ColumnInfo
		IsActualTable() bool

		Dependencies(colName string, org originable) (dependencies, error)

		IsInfSchema() bool
		GetExprFor(s string) (sqlparser.Expr, error)
		GetTables(org originable) TableSet
	}

	// ColumnInfo contains information about columns
	ColumnInfo struct {
		Name string
		Type querypb.Type
	}

	// AliasedTable contains the alias table expr and vindex table
	AliasedTable struct {
		tableName   string
		ASTNode     *sqlparser.AliasedTableExpr
		Table       *vindexes.Table
		isInfSchema bool
	}

	// VindexTable contains a vindexes.Vindex and a TableInfo. The former represents the vindex
	// we are keeping information about, and the latter represents the additional table information
	// (usually a RealTable or an AliasedTable) of our vindex.
	VindexTable struct {
		Table  TableInfo
		Vindex vindexes.Vindex
	}

	// TableSet is how a set of tables is expressed.
	// Tables get unique bits assigned in the order that they are encountered during semantic analysis
	TableSet uint64 // we can only join 64 tables with this underlying data type
	// TODO : change uint64 to struct to support arbitrary number of tables.

	// ExprDependencies stores the tables that an expression depends on as a map
	ExprDependencies map[sqlparser.Expr]TableSet

	// SemTable contains semantic analysis information about the query.
	SemTable struct {
		Tables []TableInfo
		// ProjectionErr stores the error that we got during the semantic analysis of the SelectExprs.
		// This is only a real error if we are unable to plan the query as a single route
		ProjectionErr error

		// Recursive contains the dependencies from the expression to the actual tables
		// in the query (i.e. not including derived tables). If an expression is a column on a derived table,
		// this map will contain the accumulated dependencies for the column expression inside the derived table
		Recursive ExprDependencies

		// Direct keeps information about the closest dependency for an expression.
		// It does not recurse inside derived tables and the like to find the original dependencies
		Direct ExprDependencies

		exprTypes   map[sqlparser.Expr]querypb.Type
		selectScope map[*sqlparser.Select]*scope
		Comments    sqlparser.Comments
		SubqueryMap map[*sqlparser.Select][]*subquery
		SubqueryRef map[*sqlparser.Subquery]*subquery

		// ColumnEqualities is used to enable transitive closures
		// if a == b and b == c then a == c
		ColumnEqualities map[columnName][]sqlparser.Expr
	}

	columnName struct {
		Table      TableSet
		ColumnName string
	}

	subquery struct {
		ArgName  string
		SubQuery *sqlparser.Subquery
		OpCode   engine.PulloutOpcode
	}

	scope struct {
		parent     *scope
		selectStmt *sqlparser.Select
		tables     []TableInfo
		isUnion    bool
	}

	// SchemaInformation is used tp provide table information from Vschema.
	SchemaInformation interface {
		FindTableOrVindex(tablename sqlparser.TableName) (*vindexes.Table, vindexes.Vindex, string, topodatapb.TabletType, key.Destination, error)
	}
)

var (
	// ErrMultipleTables refers to an error happening when something should be used only for single tables
	ErrMultipleTables = vterrors.Errorf(vtrpcpb.Code_INTERNAL, "[BUG] should only be used for single tables")
)

// Dependencies implements the TableInfo interface
func (v *VindexTable) Dependencies(colName string, org originable) (dependencies, error) {
	return v.Table.Dependencies(colName, org)
}

// Dependencies implements the TableInfo interface
func (a *AliasedTable) Dependencies(colName string, org originable) (dependencies, error) {
	return depsForAliasedAndRealTables(colName, org, a.ASTNode, a.GetColumns(), a.Authoritative())
}

func depsForAliasedAndRealTables(colName string, org originable, node *sqlparser.AliasedTableExpr, columns []ColumnInfo, authoritative bool) (dependencies, error) {
	ts := org.tableSetFor(node)
	for _, info := range columns {
		if strings.EqualFold(info.Name, colName) {
			return createCertain(ts, ts, &info.Type), nil
		}
	}

	if authoritative {
		return &nothing{}, nil
	}
	return createUncertain(ts, ts), nil
}

// GetTables implements the TableInfo interface
func (v *VindexTable) GetTables(org originable) TableSet {
	return v.Table.GetTables(org)
}

// GetTables implements the TableInfo interface
func (a *AliasedTable) GetTables(org originable) TableSet {
	return org.tableSetFor(a.ASTNode)
}

// GetExprFor implements the TableInfo interface
func (v *VindexTable) GetExprFor(_ string) (sqlparser.Expr, error) {
	panic("implement me")
}

// CopyDependencies copies the dependencies from one expression into the other
func (st *SemTable) CopyDependencies(from, to sqlparser.Expr) {
	st.Recursive[to] = st.RecursiveDeps(from)
	st.Direct[to] = st.DirectDeps(from)
}

// GetExprFor implements the TableInfo interface
func (a *AliasedTable) GetExprFor(s string) (sqlparser.Expr, error) {
	return nil, vterrors.Errorf(vtrpcpb.Code_INTERNAL, "Unknown column '%s' in 'field list'", s)
}

// IsInfSchema implements the TableInfo interface
func (a *AliasedTable) IsInfSchema() bool {
	return a.isInfSchema
}

// IsActualTable implements the TableInfo interface
func (a *AliasedTable) IsActualTable() bool {
	return true
}

var _ TableInfo = (*AliasedTable)(nil)
var _ TableInfo = (*VindexTable)(nil)

// GetVindexTable implements the TableInfo interface
func (v *VindexTable) GetVindexTable() *vindexes.Table {
	return v.Table.GetVindexTable()
}

func vindexTableToColumnInfo(tbl *vindexes.Table) []ColumnInfo {
	if tbl == nil {
		return nil
	}
	nameMap := map[string]interface{}{}
	cols := make([]ColumnInfo, 0, len(tbl.Columns))
	for _, col := range tbl.Columns {
		cols = append(cols, ColumnInfo{
			Name: col.Name.String(),
			Type: col.Type,
		})
		nameMap[col.Name.String()] = nil
	}
	// If table is authoritative, we do not need ColumnVindexes to help in resolving the unqualified columns.
	if tbl.ColumnListAuthoritative {
		return cols
	}
	for _, vindex := range tbl.ColumnVindexes {
		for _, column := range vindex.Columns {
			name := column.String()
			if _, exists := nameMap[name]; exists {
				continue
			}
			cols = append(cols, ColumnInfo{
				Name: name,
			})
			nameMap[name] = nil
		}
	}
	return cols
}

// GetColumns implements the TableInfo interface
func (a *AliasedTable) GetColumns() []ColumnInfo {
	return vindexTableToColumnInfo(a.Table)
}

// GetExpr implements the TableInfo interface
func (a *AliasedTable) GetExpr() *sqlparser.AliasedTableExpr {
	return a.ASTNode
}

// GetVindexTable implements the TableInfo interface
func (a *AliasedTable) GetVindexTable() *vindexes.Table {
	return a.Table
}

// Name implements the TableInfo interface
func (a *AliasedTable) Name() (sqlparser.TableName, error) {
	return a.ASTNode.TableName()
}

// Authoritative implements the TableInfo interface
func (a *AliasedTable) Authoritative() bool {
	return a.Table != nil && a.Table.ColumnListAuthoritative
}

// Matches implements the TableInfo interface
func (a *AliasedTable) Matches(name sqlparser.TableName) bool {
	return a.tableName == name.Name.String() && name.Qualifier.IsEmpty()
}

// Matches implements the TableInfo interface
func (v *VindexTable) Matches(name sqlparser.TableName) bool {
	return v.Table.Matches(name)
}

// Authoritative implements the TableInfo interface
func (v *VindexTable) Authoritative() bool {
	return true
}

// Name implements the TableInfo interface
func (v *VindexTable) Name() (sqlparser.TableName, error) {
	return v.Table.Name()
}

// GetExpr implements the TableInfo interface
func (v *VindexTable) GetExpr() *sqlparser.AliasedTableExpr {
	return v.Table.GetExpr()
}

// GetColumns implements the TableInfo interface
func (v *VindexTable) GetColumns() []ColumnInfo {
	return v.Table.GetColumns()
}

// IsActualTable implements the TableInfo interface
func (v *VindexTable) IsActualTable() bool {
	return true
}

// IsInfSchema implements the TableInfo interface
func (v *VindexTable) IsInfSchema() bool {
	return v.Table.IsInfSchema()
}

// NewSemTable creates a new empty SemTable
func NewSemTable() *SemTable {
	return &SemTable{Recursive: map[sqlparser.Expr]TableSet{}, ColumnEqualities: map[columnName][]sqlparser.Expr{}}
}

// TableSetFor returns the bitmask for this particular table
func (st *SemTable) TableSetFor(t *sqlparser.AliasedTableExpr) TableSet {
	for idx, t2 := range st.Tables {
		if t == t2.GetExpr() {
			return 1 << idx
		}
	}
	return 0
}

// TableInfoFor returns the table info for the table set. It should contains only single table.
func (st *SemTable) TableInfoFor(id TableSet) (TableInfo, error) {
	if id.NumberOfTables() > 1 {
		return nil, ErrMultipleTables
	}
	return st.Tables[id.TableOffset()], nil
}

// RecursiveDeps return the table dependencies of the expression.
func (st *SemTable) RecursiveDeps(expr sqlparser.Expr) TableSet {
	return st.Recursive.Dependencies(expr)
}

// DirectDeps return the table dependencies of the expression.
func (st *SemTable) DirectDeps(expr sqlparser.Expr) TableSet {
	return st.Direct.Dependencies(expr)
}

// AddColumnEquality adds a relation of the given colName to the ColumnEqualities map
func (st *SemTable) AddColumnEquality(colName *sqlparser.ColName, expr sqlparser.Expr) {
	ts := st.Direct.Dependencies(colName)
	columnName := columnName{
		Table:      ts,
		ColumnName: colName.Name.String(),
	}
	elem := st.ColumnEqualities[columnName]
	elem = append(elem, expr)
	st.ColumnEqualities[columnName] = elem
}

// GetExprAndEqualities returns a slice containing the given expression, and it's known equalities if any
func (st *SemTable) GetExprAndEqualities(expr sqlparser.Expr) []sqlparser.Expr {
	result := []sqlparser.Expr{expr}
	switch expr := expr.(type) {
	case *sqlparser.ColName:
		table := st.DirectDeps(expr)
		k := columnName{Table: table, ColumnName: expr.Name.String()}
		result = append(result, st.ColumnEqualities[k]...)
	}
	return result
}

// TableInfoForExpr returns the table info of the table that this expression depends on.
// Careful: this only works for expressions that have a single table dependency
func (st *SemTable) TableInfoForExpr(expr sqlparser.Expr) (TableInfo, error) {
	return st.TableInfoFor(st.Direct.Dependencies(expr))
}

// GetSelectTables returns the table in the select.
func (st *SemTable) GetSelectTables(node *sqlparser.Select) []TableInfo {
	scope := st.selectScope[node]
	return scope.tables
}

// AddExprs adds new select exprs to the SemTable.
func (st *SemTable) AddExprs(tbl *sqlparser.AliasedTableExpr, cols sqlparser.SelectExprs) {
	tableSet := st.TableSetFor(tbl)
	for _, col := range cols {
		st.Recursive[col.(*sqlparser.AliasedExpr).Expr] = tableSet
	}
}

// TypeFor returns the type of expressions in the query
func (st *SemTable) TypeFor(e sqlparser.Expr) *querypb.Type {
	typ, found := st.exprTypes[e]
	if found {
		return &typ
	}
	return nil
}

// Dependencies return the table dependencies of the expression. This method finds table dependencies recursively
func (d ExprDependencies) Dependencies(expr sqlparser.Expr) TableSet {
	deps, found := d[expr]
	if found {
		return deps
	}

	// During the original semantic analysis, all ColName:s were found and bound the the corresponding tables
	// Here, we'll walk the expression tree and look to see if we can found any sub-expressions
	// that have already set dependencies.
	_ = sqlparser.Walk(func(node sqlparser.SQLNode) (kontinue bool, err error) {
		expr, ok := node.(sqlparser.Expr)
		if !ok || !validAsMapKey(expr) {
			// if this is not an expression, or it is an expression we can't use as a map-key,
			// just carry on down the tree
			return true, nil
		}

		set, found := d[expr]
		if found {
			deps |= set
		}

		// if we found a cached value, there is no need to continue down to visit children
		return !found, nil
	}, expr)

	d[expr] = deps
	return deps
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent}
}

func (s *scope) addTable(info TableInfo) error {
	name, err := info.Name()
	if err != nil {
		return err
	}
	tblName := name.Name.String()
	for _, table := range s.tables {
		name, err := table.Name()
		if err != nil {
			return err
		}

		if tblName == name.Name.String() {
			return vterrors.NewErrorf(vtrpcpb.Code_INVALID_ARGUMENT, vterrors.NonUniqTable, "Not unique table/alias: '%s'", name.Name.String())
		}
	}
	s.tables = append(s.tables, info)
	return nil
}

// IsOverlapping returns true if at least one table exists in both sets
func (ts TableSet) IsOverlapping(b TableSet) bool { return ts&b != 0 }

// IsSolvedBy returns true if all of `ts` is contained in `b`
func (ts TableSet) IsSolvedBy(b TableSet) bool { return ts&b == ts }

// NumberOfTables returns the number of bits set
func (ts TableSet) NumberOfTables() int {
	// Brian Kernighan’s Algorithm
	count := 0
	for ts > 0 {
		ts &= ts - 1
		count++
	}
	return count
}

// TableOffset returns the offset in the Tables array from TableSet
func (ts TableSet) TableOffset() int {
	offset := 0
	for ts > 1 {
		ts = ts >> 1
		offset++
	}
	return offset
}

// Constituents returns an slice with all the
// individual tables in their own TableSet identifier
func (ts TableSet) Constituents() (result []TableSet) {
	mask := ts

	for mask > 0 {
		maskLeft := mask & (mask - 1)
		constituent := mask ^ maskLeft
		mask = maskLeft
		result = append(result, constituent)
	}
	return
}

// Merge creates a TableSet that contains both inputs
func (ts TableSet) Merge(other TableSet) TableSet {
	return ts | other
}

// RewriteDerivedExpression rewrites all the ColName instances in the supplied expression with
// the expressions behind the column definition of the derived table
// SELECT foo FROM (SELECT id+42 as foo FROM user) as t
// We need `foo` to be translated to `id+42` on the inside of the derived table
func RewriteDerivedExpression(expr sqlparser.Expr, vt TableInfo) (sqlparser.Expr, error) {
	newExpr := sqlparser.CloneExpr(expr)
	sqlparser.Rewrite(newExpr, func(cursor *sqlparser.Cursor) bool {
		switch node := cursor.Node().(type) {
		case *sqlparser.ColName:
			exp, err := vt.GetExprFor(node.Name.String())
			if err != nil {
				return false
			}
			cursor.Replace(exp)
			return false
		}
		return true
	}, nil)
	return newExpr, nil
}
