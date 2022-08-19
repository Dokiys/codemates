package conv

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

var emptyIgnoreMap = make(ignoreMap)

type ignoreMap map[string]struct{}

func (i ignoreMap) pickField(stmts []ast.Stmt, xname string) {
	for _, stmt := range stmts {
		switch x := stmt.(type) {
		case *ast.AssignStmt:
			for _, lh := range x.Lhs {
				// check is result assigned
				se, ok := lh.(*ast.SelectorExpr)
				if !ok {
					continue
				}

				ident, ok := se.X.(*ast.Ident)
				if ident.Name != xname {
					continue
				}

				i[se.Sel.Name] = struct{}{}
			}

		default:

		}
	}
}
func (i ignoreMap) exist(name string) bool {
	_, ok := i[name]
	return ok
}

// TODO[Dokiy] 2022/8/12: notePosition reference wire[https://github.com/google/wire/blob/d07cde0df9c5edd46e05e21d29eb315e0b452cbc/internal/wire/errors.go#L60]
type fnConv struct {
	pkg *packages.Package
	syn *ast.File
	fd  *ast.FuncDecl

	impAlias map[string]string
	// params
	param      *ast.Field
	paramName  string
	result     *ast.Field
	resultName string

	// stmt
	ignore    ignoreMap
	convStmt  []ast.Stmt
	panicStmt []ast.Stmt
}

// newFnConv
func newFnConv(pkg *packages.Package, syn *ast.File, fd *ast.FuncDecl) (*fnConv, bool) {
	// check is handle func
	if fd.Recv != nil {
		return nil, false
	}
	// Make sure one param which name is not '_' and one result
	if len(fd.Type.Params.List) <= 0 || fd.Type.Params.List[0].Names[0].Name == "_" || len(fd.Type.Results.List) != 1 {
		// NOTE[Dokiy] 2022/8/16: notePosition
		return nil, false
	}

	// Get result name
	param, result := fd.Type.Params.List[0], fd.Type.Results.List[0]
	if result.Names == nil {
		ast.Inspect(result, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok {
				// TODO[Dokiy] 2022/8/17: pkg name
				result.Names = []*ast.Ident{ast.NewIdent("gconv" + ident.Name)}
				return false
			}
			return true
		})

	}

	importAlias := make(map[string]string, len(syn.Imports)+1)
	for _, imp := range syn.Imports {
		alias := strings.Trim(imp.Path.Value, "\"")
		// Named import
		if imp.Name != nil {
			importAlias[alias] = strings.Trim(imp.Name.Name, "\"")
			continue
		}

		// Add last name
		index := strings.LastIndex(imp.Path.Value, "/")
		if index < 0 {
			index = 0
		}

		importAlias[alias] = strings.Trim(imp.Path.Value[index+1:], "\"")
	}

	return &fnConv{
		pkg:        pkg,
		fd:         fd,
		ignore:     make(ignoreMap),
		impAlias:   importAlias,
		param:      param,
		paramName:  param.Names[0].Name,
		result:     result,
		resultName: result.Names[0].Name,
	}, true
}

func (f *fnConv) typeOf(e ast.Expr) types.Type {
	return f.pkg.TypesInfo.TypeOf(e)
}

func (f *fnConv) typeOfResult() types.Type {
	return f.typeOf(f.result.Type)
}

func (f *fnConv) typeOfParam() types.Type {
	return f.typeOf(f.param.Type)
}

func (f *fnConv) add(stmt ...ast.Stmt) {
	for _, s := range stmt {
		if s != nil {
			f.convStmt = append(f.convStmt, s)
		}
	}
}

func (f *fnConv) convField(resultName, paramName string) (stmt []ast.Stmt) {
	// Conv fields
	pMap, rFields := getFieldsMap(f.typeOfParam(), emptyIgnoreMap), getFields(f.typeOfResult(), f.ignore)

	for _, rf := range rFields {
		pf, ok := pMap[rf.Name()]
		if !ok || rf.Name() != pf.Name() {
			continue
		}
		stmt = append(stmt, genVarConv(rf, pf, resultName, paramName)...)
	}
	return stmt
}

func (f *fnConv) replace() {
	// Load panic stmt
	f.loadPanicStmt()

	// Add init result stmt
	f.add(f.resultInitStmt())
	f.add(f.convField(f.resultName, f.paramName)...)
	// TODO[Dokiy] 2022/8/19: Slice for
	//for i = 0; i < len(params); i++ {
	//	result[i].id = params[i].id
	//}

	// Replace func content
	astutil.Apply(f.fd, func(c *astutil.Cursor) bool {
		switch c.Node().(type) {
		case *ast.BlockStmt:
			c.Replace(&ast.BlockStmt{
				Lbrace: token.NoPos,
				List:   f.content(),
				Rbrace: token.NoPos,
			})

			return false
		}

		return true
	}, nil)
	return
}

func (f *fnConv) resultInitStmt() ast.Stmt {
	vi := newVarIniter(f.impAlias, f.typeOfResult(), setSliceLen(fmt.Sprintf("len(%s)", f.paramName)))
	return &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(f.resultName)},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{ast.NewIdent(vi.initIdent())},
	}
}

// loadStmt load last panic content.
func (f *fnConv) loadPanicStmt() {
	var stmts []ast.Stmt
	for i, stmt := range f.fd.Body.List {
		switch stmt := stmt.(type) {
		case *ast.EmptyStmt:
			// Do nothing.
		case *ast.ReturnStmt:
			// Do nothing.
		case *ast.ExprStmt:
			call, ok := stmt.X.(*ast.CallExpr)
			if !ok {
				continue
			}

			convObj := qualifiedIdentObject(f.pkg.TypesInfo, call.Fun)
			if convObj != types.Universe.Lookup("panic") || i+1 != len(f.fd.Body.List) {
				continue
			}

			// Handle last panic only.
			for _, arg := range call.Args {
				fl, ok := arg.(*ast.FuncLit)
				if !ok {
					continue
				}

				stmts = append(stmts, fl.Body.List...)
			}
		}
	}

	f.panicStmt = stmts
	f.ignore.pickField(stmts, f.resultName)
	return
}

func (f *fnConv) content() (stmt []ast.Stmt) {
	stmt = append(f.convStmt, f.panicStmt...)
	return append(stmt, &ast.ReturnStmt{})
}

func getFields(tpy types.Type, ignore ignoreMap) []*types.Var {
	var fields []*types.Var
	for {
		switch x := underPointerTpy(tpy).(type) {
		case *types.Struct:
			for i := 0; i < x.NumFields(); i++ {
				field := x.Field(i)

				if ignore.exist(field.Name()) || !field.Exported() {
					continue
				}

				//fields[field.Name()] = field
				fields = append(fields, field)
			}
			return fields
		case *types.Slice:
			tpy = x.Elem()

		case *types.Array:
			tpy = x.Elem()

		case *types.Basic:
			return fields

		default:
			// TODO[Dokiy] 2022/8/12: notePosition
			panic("Unsupported params")
		}
	}
}

func getFieldsMap(tpy types.Type, ignore ignoreMap) map[string]*types.Var {
	var fields = make(map[string]*types.Var)
	for {
		switch x := underPointerTpy(tpy).(type) {
		case *types.Struct:
			for i := 0; i < x.NumFields(); i++ {
				field := x.Field(i)

				if ignore.exist(field.Name()) || !field.Exported() {
					continue
				}

				fields[field.Name()] = field
				//fields = append(fields, field)
			}
			return fields
		// TODO[Dokiy] 2022/8/12:
		//case *types.Slice:
		//tpy = x.Elem().Underlying()
		//continue

		default:
			// TODO[Dokiy] 2022/8/12: notePosition
			panic("Unsupported params")
		}
	}
}
