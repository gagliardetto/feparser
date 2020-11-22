package feparser

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gagliardetto/codebox/scanner"
	. "github.com/gagliardetto/utilz"
	"golang.org/x/tools/go/packages"
)

func Load(pk *scanner.Package) (*FEPackage, error) {
	fePackage := &FEPackage{
		Funcs:            make([]*FEFunc, 0),
		TypeMethods:      make([]*FETypeMethod, 0),
		InterfaceMethods: make([]*FEInterfaceMethod, 0),
		Structs:          make([]*FEStruct, 0),
	}

	fePackage.Module = scanModule(pk.Module)

	{
		fePackage.ID = FormatID("package", pk.Path)
		fePackage.ClassName = FormatCodeQlName(pk.Path)
		fePackage.PkgPath = scanner.RemoveGoSrcClonePath(pk.Path)
		fePackage.PkgName = pk.Name

		for _, fn := range pk.Funcs {
			if fn.Receiver == nil {
				f := getFEFunc(fn)
				// TODO: what to do with aliases???
				f.PkgPath = fePackage.PkgPath
				fePackage.Funcs = append(fePackage.Funcs, f)
			}
		}
		for _, mt := range pk.Methods {
			meth := getFETypeMethod(fePackage.PkgPath, mt, pk.Funcs)
			if meth != nil {
				fePackage.TypeMethods = append(fePackage.TypeMethods, meth)
			}
		}
		for _, it := range pk.Interfaces {
			fePackage.InterfaceMethods = append(fePackage.InterfaceMethods, getAllFEInterfaceMethods(it)...)
		}

		for _, str := range pk.Structs {
			fePackage.Structs = append(fePackage.Structs, scanStruct(str))
		}
	}

	// Sort funcs by name:
	sort.Slice(fePackage.Funcs, func(i, j int) bool {
		return fePackage.Funcs[i].Name < fePackage.Funcs[j].Name
	})
	// Sort type methods by receiver:
	sort.Slice(fePackage.TypeMethods, func(i, j int) bool {
		// If same receiver...
		if fePackage.TypeMethods[i].Receiver.QualifiedName == fePackage.TypeMethods[j].Receiver.QualifiedName {
			// ... sort by func name:
			return fePackage.TypeMethods[i].Func.Name < fePackage.TypeMethods[j].Func.Name
		}
		return fePackage.TypeMethods[i].Receiver.QualifiedName < fePackage.TypeMethods[j].Receiver.QualifiedName
	})
	// Sort interface methods by receiver:
	sort.Slice(fePackage.InterfaceMethods, func(i, j int) bool {
		// If same receiver...
		if fePackage.InterfaceMethods[i].Receiver.QualifiedName == fePackage.InterfaceMethods[j].Receiver.QualifiedName {
			// ... sort by func name:
			return fePackage.InterfaceMethods[i].Func.Name < fePackage.InterfaceMethods[j].Func.Name
		}
		return fePackage.InterfaceMethods[i].Receiver.QualifiedName < fePackage.InterfaceMethods[j].Receiver.QualifiedName
	})
	// Sort structs by name:
	sort.Slice(fePackage.Structs, func(i, j int) bool {
		return fePackage.Structs[i].TypeString < fePackage.Structs[j].TypeString
	})

	{ // Deduplicate:
		fePackage.Funcs = DeduplicateSlice(fePackage.Funcs, func(i int) string {
			return fePackage.Funcs[i].Signature
		}).([]*FEFunc)

		fePackage.TypeMethods = DeduplicateSlice(fePackage.TypeMethods, func(i int) string {
			return fePackage.TypeMethods[i].Func.Signature
		}).([]*FETypeMethod)

		fePackage.InterfaceMethods = DeduplicateSlice(fePackage.InterfaceMethods, func(i int) string {
			return fePackage.InterfaceMethods[i].Func.Signature
		}).([]*FEInterfaceMethod)

		fePackage.Structs = DeduplicateSlice(fePackage.Structs, func(i int) string {
			return fePackage.Structs[i].TypeString
		}).([]*FEStruct)
	}
	return fePackage, nil
}

type Identity struct {
	Element    Element
	Index      int
	IsVariadic bool
}
type CodeQlIdentity struct {
	Placeholder string
	Identity
}
type FEPackage struct {
	PkgPath    string
	PkgName    string
	ID         string
	ClassName  string
	IsStandard bool

	Module           *Module
	Funcs            []*FEFunc
	TypeMethods      []*FETypeMethod
	InterfaceMethods []*FEInterfaceMethod
	Structs          []*FEStruct
}

func scanModule(mod *packages.Module) *Module {
	if mod == nil {
		return nil
	}
	res := &Module{
		Path:      mod.Path,
		Version:   mod.Version,
		Time:      mod.Time,
		Main:      mod.Main,
		GoVersion: mod.GoVersion,
	}
	return res
}

type Module struct {
	Path      string
	Version   string
	GoVersion string
	Time      *time.Time
	Main      bool
}

type FEFunc struct {
	CodeQL    *CodeQlFinalVals
	ClassName string
	Signature string
	ID        string `json:",omitempty"` // NOTE: no ID for funcs in methods (on interfaces, on types); only funcs have a complete ID.
	Documentation
	Name    string
	PkgPath string
	PkgName string

	Parameters []*FEType
	Results    []*FEType
	original   *scanner.Func
}

//
func (v *FEFunc) Len() int {
	return len(v.Parameters) + len(v.Results)
}

// GetRelativeElement: provided an absolute index, the GetRelativeElement function
// returns the element it corresponds to, along with the relative index
// of that kind of element.
func (v *FEFunc) GetRelativeElement(index int) (interface{}, int, error) {
	if index >= v.Len() {
		return nil, 0, errors.New("index outside of bounds")
	}
	// Is it a parameter?
	if index < len(v.Parameters) {
		relIndex := index
		return v.Parameters[relIndex], relIndex, nil
	}
	// Is it a result?
	if index >= len(v.Parameters) {
		relIndex := index - (len(v.Parameters))
		return v.Results[relIndex], relIndex, nil
	}
	return nil, 0, errors.New("nothing selected")
}

func (v *FEFunc) GetOriginal() *scanner.Func {
	return v.original
}

type Element string

const (
	ElementReceiver  Element = "receiver"
	ElementParameter Element = "parameter"
	ElementResult    Element = "result"
)

type FETypeMethod struct {
	CodeQL    *CodeQlFinalVals
	ClassName string
	Documentation
	IsOnPtr  bool
	Receiver *FEReceiver
	ID       string
	Func     *FEFunc
	original types.Type
}

//
func (v *FETypeMethod) Len() int {
	l := 1 + len(v.Func.Parameters) + len(v.Func.Results)
	return l
}

// GetRelativeElement: provided an absolute index, the GetRelativeElement function
// returns the element it corresponds to, along with the relative index
// of that kind of element.
func (v *FETypeMethod) GetRelativeElement(index int) (interface{}, int, error) {
	if index >= v.Len() {
		return nil, 0, errors.New("index outside of bounds")
	}
	// Is it the receiver?
	if index == 0 {
		return v.Receiver, 0, nil
	}
	// Is it a parameter?
	if index > 0 && index < 1+len(v.Func.Parameters) {
		relIndex := index - 1
		return v.Func.Parameters[relIndex], relIndex, nil
	}
	// Is it a result?
	if index >= 1+len(v.Func.Parameters) {
		relIndex := index - (1 + len(v.Func.Parameters))
		return v.Func.Results[relIndex], relIndex, nil
	}
	return nil, 0, errors.New("nothing selected")
}

func (v *FETypeMethod) GetOriginal() types.Type {
	return v.original
}

type FEInterfaceMethod FETypeMethod

//
func (v *FEInterfaceMethod) Len() int {
	return FEIToFET(v).Len()
}

// GetRelativeElement: provided an absolute index, the GetRelativeElement function
// returns the element it corresponds to, along with the relative index
// of that kind of element.
func (v *FEInterfaceMethod) GetRelativeElement(index int) (interface{}, int, error) {
	return FEIToFET(v).GetRelativeElement(index)
}

type FEReceiver struct {
	FEType
	original types.Type
}

func (v *FEReceiver) GetOriginal() types.Type {
	return v.original
}

var ValidElementNames = []string{
	string(ElementReceiver),
	string(ElementParameter),
	string(ElementResult),
}

func IsValidElementName(name Element) bool {
	return IsAnyOf(
		string(name),
		ValidElementNames...,
	)
}

func NewCodeQlFinalVals() *CodeQlFinalVals {
	return &CodeQlFinalVals{}
}
func (obj *CodeQlFinalVals) Validate() error {
	if obj.Blocks == nil || len(obj.Blocks) == 0 {
		return errors.New("obj.Blocks is not set")
	}
	if err := ValidateBlocksAreActive(obj.Blocks...); err != nil {
		return err
	}

	return nil
}

type CodeQlFinalVals struct {
	// Generated generated contains the generated class:
	GeneratedClass string `json:"GeneratedClass,omitempty"`
	// GeneratedConditions contains the generated conditions of the flow:
	GeneratedConditions string `json:"GeneratedConditions,omitempty"`
	Blocks              []*FlowBlock
	IsEnabled           bool
	//Pointers            *CodeQLPointers // Pointers is where the current pointers will be stored
}

type DEPRECATEDFEModule struct {
	Funcs            []*DEPRECATEDFEFunc
	TypeMethods      []*DEPRECATEDFETypeMethod
	InterfaceMethods []*DEPRECATEDFETypeMethod
}

type DEPRECATEDFEFunc struct {
	CodeQL    *DEPRECATEDCodeQlFinalVals
	Signature string
}
type DEPRECATEDFETypeMethod struct {
	CodeQL *DEPRECATEDCodeQlFinalVals
	Func   *DEPRECATEDFEFunc
}
type DEPRECATEDCodeQlFinalVals struct {
	IsEnabled bool
	Pointers  *CodeQLPointers // Pointers is where the current pointers will be stored
}

func FEIToFET(feIt *FEInterfaceMethod) *FETypeMethod {
	converted := FETypeMethod(*feIt)
	return &converted
}

// ShouldUseAlias tells whether the package name and the base
// of the backage path are the same; if they are not,
// then the package should use an alias in the import.
func ShouldUseAlias(pkgPath string, pkgName string) bool {
	return filepath.Base(pkgPath) != pkgName
}

const TODO = "TODO"

type CodeQLPointers struct {
	Inp  *CodeQlIdentity
	Outp *CodeQlIdentity
}

func (obj *CodeQLPointers) Validate() error {
	if obj.Inp == nil {
		return errors.New("obj.Inp is not set")
	}
	if obj.Outp == nil {
		return errors.New("obj.Outp is not set")
	}

	if err := obj.Inp.Identity.Validate(); err != nil {
		return err
	}
	if err := obj.Outp.Validate(); err != nil {
		return err
	}

	if obj.Inp.Identity.Element == obj.Outp.Identity.Element && (obj.Inp.Identity.Element == ElementReceiver || (obj.Inp.Identity.Index == obj.Outp.Identity.Index)) {
		return errors.New("obj.Inp and obj.Outp have same values")
	}

	return nil
}
func (obj *Identity) Validate() error {
	if obj.Element == "" || obj.Element == TODO || !IsValidElementName(obj.Element) {
		return errors.New("obj.Element is not set")
	}

	// the Index can be non-valid only for the receiver:
	if obj.Index < 0 && obj.Element != ElementReceiver {
		return errors.New("obj.Index is not set")
	}
	return nil
}

type FEType struct {
	Identity *CodeQlIdentity `json:",omitempty"`
	Documentation

	VarName       string `json:",omitempty"`
	TypeName      string
	PkgName       string `json:",omitempty"`
	PkgPath       string `json:",omitempty"`
	QualifiedName string `json:",omitempty"`
	IsPtr         bool
	IsBasic       bool
	IsVariadic    bool
	IsNullable    bool
	IsStruct      bool
	TypeString    string
	KindString    string
	original      scanner.Type
}

func (v *FEType) GetOriginal() scanner.Type {
	return v.original
}

func getFEType(tp scanner.Type, pkgPath string) *FEType {
	var fe FEType
	fe.original = tp

	fe.IsVariadic = tp.IsVariadic()
	fe.IsNullable = tp.IsNullable()
	fe.IsPtr = tp.IsPtr()
	fe.IsStruct = tp.IsStruct()
	fe.IsBasic = tp.IsBasic()

	sl, ok := tp.GetType().(*types.Slice)
	if tp.IsVariadic() && ok {
		fe.TypeString = "..." + types.TypeString(sl.Elem(), RelativeTo(pkgPath))
	} else {
		fe.TypeString = types.TypeString(tp.GetType(), RelativeTo(pkgPath))
	}
	fe.KindString = FormatKindString(tp.GetType())

	if tp.GetTypesVar() != nil {
		varName := tp.GetTypesVar().Name()
		if varName != "" {
			fe.VarName = varName
		}
		finalType := tp.GetTypesVar().Type()
		{
			slice, ok := tp.GetTypesVar().Type().(*types.Slice)
			if ok {
				finalType = slice.Elem()
			}
		}
		{
			array, ok := tp.GetTypesVar().Type().(*types.Array)
			if ok {
				finalType = array.Elem()
			}
		}
		// Check if pointer:
		{
			pointer, ok := finalType.(*types.Pointer)
			if ok {
				finalType = pointer.Elem()
			}
		}
		{
			named, ok := finalType.(*types.Named)
			if ok {
				fe.TypeName = named.Obj().Name()
				if pkg := named.Obj().Pkg(); pkg != nil {
					fe.QualifiedName = scanner.StringRemoveGoPath(pkg.Path()) + "." + named.Obj().Name()
					fe.PkgPath = scanner.RemoveGoPath(named.Obj().Pkg())
					fe.PkgName = named.Obj().Pkg().Name()
				}
			} else {
				if _, ok := tp.(*scanner.BaseType); !ok {
					fe.TypeName = tp.TypeString()
				}
			}
		}
	}

	return &fe
}

func getFETypeMethod(pkgPath string, mt *types.Selection, allFuncs []*scanner.Func) *FETypeMethod {
	var fe FETypeMethod

	fe.CodeQL = NewCodeQlFinalVals()

	fe.Receiver = &FEReceiver{}
	fe.Receiver.Identity = &CodeQlIdentity{
		Placeholder: "isReceiver()",
		Identity: Identity{
			Element: ElementReceiver,
			Index:   -1,
		},
	}

	fe.Receiver.TypeString = types.TypeString(mt.Recv(), RelativeTo(pkgPath))
	fe.Receiver.KindString = FormatKindString(mt.Recv())

	{
		var named *types.Named
		ptr, isPtr := mt.Recv().(*types.Pointer)
		if isPtr {
			named = ptr.Elem().(*types.Named)
		} else {
			named = mt.Recv().(*types.Named)
		}
		fe.Receiver.IsPtr = isPtr
		{
			// TODO:
			//fe.Receiver.IsVariadic = tp.IsVariadic()
			//fe.Receiver.IsNullable = tp.IsNullable()
			//fe.Receiver.IsPtr = tp.IsPtr()
			//fe.Receiver.IsStruct = tp.IsStruct()
			//fe.Receiver.IsBasic = tp.IsBasic()
		}
		fe.Receiver.original = named
		fe.Receiver.TypeName = named.Obj().Name()
		fe.Receiver.QualifiedName = scanner.RemoveGoPath(named.Obj().Pkg()) + "." + named.Obj().Name()
		fe.Receiver.PkgPath = scanner.RemoveGoPath(named.Obj().Pkg())
		fe.Receiver.PkgName = named.Obj().Pkg().Name()
		//fe.Receiver.VarName =
	}
	// Skip methods on non-exported types:
	if !token.IsExported(fe.Receiver.TypeName) {
		return nil
	}

	fe.Func = &FEFunc{}
	methodFuncName := mt.Obj().Name()

	{
		// Check if the method is on a pointer of a value:
		_, isPtr := mt.Obj().Type().(*types.Signature).Recv().Type().(*types.Pointer)
		if isPtr {
			fe.IsOnPtr = true
		}
	}
	{
		findCorrespondingFunc := func() bool {
			for _, mtFn := range allFuncs {
				if mtFn.Receiver != nil {

					sameReceiverType := fe.Receiver.QualifiedName == mtFn.Receiver.TypeString()
					sameFuncName := methodFuncName == mtFn.Name

					if sameReceiverType && sameFuncName {
						fe.Documentation = getDocumentation(mtFn.Docs)
						fe.Func = getFEFunc(mtFn)
						fe.Func.ID = ""
						fe.Func.CodeQL = nil
						fe.original = mtFn.GetType()
						return true
					}
				}
			}
			return false
		}

		found := findCorrespondingFunc()
		if !found {
			return nil
		}
	}

	fe.ID = FormatID("type", "method", fe.Receiver.TypeName, methodFuncName)
	fe.ClassName = FormatCodeQlName(fe.Receiver.TypeName + "-" + methodFuncName)

	{
		width := 1 + len(fe.Func.Parameters) + len(fe.Func.Results)
		fe.CodeQL.Blocks = make([]*FlowBlock, 0)
		fe.CodeQL.Blocks = append(
			fe.CodeQL.Blocks,
			&FlowBlock{
				Inp:  make([]bool, width),
				Outp: make([]bool, width),
			},
		)
	}
	return &fe
}

func getFEInterfaceMethod(it *scanner.Interface, methodFunc *scanner.Func) *FETypeMethod {
	var fe FETypeMethod
	fe.original = it.GetType()

	fe.CodeQL = NewCodeQlFinalVals()

	fe.Receiver = &FEReceiver{}
	fe.Receiver.Identity = &CodeQlIdentity{
		Placeholder: "isReceiver()",
		Identity: Identity{
			Element: ElementReceiver,
			Index:   -1,
		},
	}

	feFunc := getFEFunc(methodFunc)
	feFunc.ID = ""
	feFunc.CodeQL = nil
	{
		fe.Receiver.original = it.GetType()
		fe.Receiver.TypeName = it.Name
		fe.Receiver.QualifiedName = scanner.StringRemoveGoPath(feFunc.PkgPath) + "." + it.Name
		fe.Receiver.PkgPath = scanner.StringRemoveGoPath(feFunc.PkgPath)
		fe.Receiver.PkgName = feFunc.PkgName

		fe.Receiver.TypeString = it.Name // TODO: is this correct?
		fe.Receiver.KindString = FormatKindString(it.GetType())
	}

	fe.Func = &FEFunc{}
	methodFuncName := feFunc.Name

	{
		// Check if the method is on a pointer of a value:
		fe.IsOnPtr = true
	}
	{
		fe.Documentation = getDocumentation(methodFunc.Docs)
		fe.Func = feFunc
	}

	fe.ID = FormatID("interface", "method", fe.Receiver.TypeName, methodFuncName)
	fe.ClassName = FormatCodeQlName(fe.Receiver.TypeName + "-" + methodFuncName)

	{
		width := 1 + len(fe.Func.Parameters) + len(fe.Func.Results)
		fe.CodeQL.Blocks = make([]*FlowBlock, 0)
		fe.CodeQL.Blocks = append(
			fe.CodeQL.Blocks,
			&FlowBlock{
				Inp:  make([]bool, width),
				Outp: make([]bool, width),
			},
		)
	}
	return &fe
}
func getAllFEInterfaceMethods(it *scanner.Interface) []*FEInterfaceMethod {

	feInterfaces := make([]*FEInterfaceMethod, 0)
	for _, mt := range it.Methods {

		feMethod := getFEInterfaceMethod(it, mt)
		converted := FEInterfaceMethod(*feMethod)
		feInterfaces = append(feInterfaces, &converted)
	}
	return feInterfaces
}

type FlowBlock struct {
	Inp  []bool
	Outp []bool
}

type IdentityGetter func(block *FlowBlock) ([]*CodeQlIdentity, []*CodeQlIdentity, error)

func FormatCodeQlName(name string) string {
	return ToCamel(strings.ReplaceAll(name, "\"", ""))
}
func FormatID(parts ...string) string {
	var outComponents []string

	for _, part := range parts {
		outComponents = append(outComponents, ToCamel(strings.ReplaceAll(part, "\"", "")))
	}
	return strings.Join(outComponents, "-")
}

func ValidateBlocksAreActive(blocks ...*FlowBlock) error {
	if len(blocks) == 0 {
		return errors.New("no blocks provided")
	}
	for blockIndex, block := range blocks {
		if AllFalse(block.Inp...) {
			return fmt.Errorf("error: Inp of block %v is all false", blockIndex)
		}
		if AllFalse(block.Outp...) {
			return fmt.Errorf("error: Outp of block %v is all false", blockIndex)
		}
	}
	return nil
}

func FormatKindString(typ types.Type) string {
	buf := new(bytes.Buffer)
	switch t := typ.(type) {
	case *types.Basic:
		{
			buf.WriteString(Sf(
				"a basic %s",
				t.String(),
			))
		}
	case *types.Array:
		{
			buf.WriteString(Sf(
				"an array [%v] of %s (i.e. %s)",
				t.Len(),
				FormatKindString(t.Elem()),
				t.String(),
			))
		}
	case *types.Slice:
		{
			buf.WriteString(Sf(
				"a slice of %s (i.e. %s)",
				FormatKindString(t.Elem()),
				t.String(),
			))
		}
	case *types.Struct:
		{
			buf.WriteString(Sf(
				"a struct",
			))
		}
	case *types.Pointer:
		{
			buf.WriteString(Sf(
				"a pointer to %s",
				FormatKindString(t.Elem()),
			))
		}
	case *types.Tuple:
		{
			buf.WriteString(Sf(
				"a tuple `%s`",
				t.String(),
			))
		}
	case *types.Signature:
		{
			buf.WriteString(Sf(
				"a signature `%s`",
				t.String(),
			))
		}
	case *types.Interface:
		{
			buf.WriteString(Sf(
				"an interface",
			))
		}
	case *types.Map:
		{
			buf.WriteString(Sf(
				"a map with key %s, and value %s (i.e. %s)",
				FormatKindString(t.Key()),
				FormatKindString(t.Elem()),
				t.String(),
			))
		}
	case *types.Chan:
		{
			buf.WriteString(Sf(
				"a chan `%s`",
				t.String(),
			))
		}
	case *types.Named:
		{
			buf.WriteString(Sf(
				"a named %s, which is %s",
				t.String(),
				FormatKindString(t.Underlying()),
			))
		}
	}

	return buf.String()
}

func getFEFunc(fn *scanner.Func) *FEFunc {
	var fe FEFunc
	fe.original = fn
	fe.CodeQL = NewCodeQlFinalVals()
	fe.ClassName = FormatCodeQlName(fn.Name)
	fe.Name = fn.Name
	fe.PkgName = fn.PkgName
	fe.ID = FormatID("function", fn.Name)
	fe.Documentation = getDocumentation(fn.Docs)
	fe.Signature = RemoveThisPackagePathFromSignature(fn.Signature, fn.PkgPath)
	fe.PkgPath = fn.PkgPath
	for i, in := range fn.Input {
		v := getFEType(in, fn.PkgPath)

		placeholder := Sf("isParameter(%v)", i)
		if v.IsVariadic {
			if len(fn.Input) == 1 {
				placeholder = "isParameter(_)"
			} else {
				placeholder = Sf("isParameter(any(int i | i >= %v))", i)
			}
		}
		isNotLast := i != len(fn.Input)-1
		if v.IsVariadic && isNotLast {
			panic(Sf("parameter %v is variadic but is NOT the last parameter", v))
		}
		v.Identity = &CodeQlIdentity{
			Placeholder: placeholder,
			Identity: Identity{
				Element:    ElementParameter,
				Index:      i,
				IsVariadic: v.IsVariadic,
			},
		}
		fe.Parameters = append(fe.Parameters, v)
	}
	for i, out := range fn.Output {
		v := getFEType(out, fn.PkgPath)

		placeholder := Sf("isResult(%v)", i)
		if len(fn.Output) == 1 {
			placeholder = "isResult()"
		}
		v.Identity = &CodeQlIdentity{
			Placeholder: placeholder,
			Identity: Identity{
				Element:    ElementResult,
				Index:      i,
				IsVariadic: v.IsVariadic,
			},
		}
		fe.Results = append(fe.Results, v)
	}
	{
		width := len(fe.Parameters) + len(fe.Results)
		fe.CodeQL.Blocks = make([]*FlowBlock, 0)
		fe.CodeQL.Blocks = append(
			fe.CodeQL.Blocks,
			&FlowBlock{
				Inp:  make([]bool, width),
				Outp: make([]bool, width),
			},
		)
	}
	return &fe
}
func RemoveThisPackagePathFromSignature(signature string, pkgPath string) string {
	clean := strings.Replace(signature, pkgPath+".", "", -1)
	return clean
}

func scanStruct(st *scanner.Struct) *FEStruct {
	// TODO: don't scan embedded structs; either they are in this package (and I'll find them in this list),
	// or they are in another package (and they are not a problem now).
	var fe = FEStruct{
		FEType: &FEType{},
	}
	fe.Fields = make([]*FEField, 0)
	fe.Documentation = getDocumentation(st.Docs)

	fe.original = st

	// Get more type info:
	{
		named := st.Type
		if named != nil && named.Obj() != nil {
			fe.TypeName = named.Obj().Name()
			if pkg := named.Obj().Pkg(); pkg != nil {
				fe.QualifiedName = scanner.StringRemoveGoPath(pkg.Path()) + "." + named.Obj().Name()
				fe.PkgPath = scanner.RemoveGoPath(named.Obj().Pkg())
				fe.PkgName = named.Obj().Pkg().Name()
			}
		}
	}
	{
		// TODO: ignore anonymous structs?
		anon := st.AnonymousType
		if anon != nil && st.AnonymousType.String() != "" {
			fe.TypeName = st.AnonymousType.String()
		}
	}

	// Get basic type info:
	{
		fe.IsStruct = st.IsStruct() // always true

		fe.TypeString = types.TypeString(st.GetType(), RelativeTo(fe.PkgPath))
		fe.KindString = FormatKindString(st.GetType())
	}

	fe.ID = FormatID("struct", fe.TypeName)
	for _, field := range st.Fields {
		feField := FEField{}
		feField.FEType = getFEType(field.Type, fe.PkgPath)
		feField.ID = FormatID("struct", "field", fe.TypeName, feField.VarName)
		feField.Documentation = getDocumentation(field.Docs)
		fe.Fields = append(fe.Fields, &feField)
	}

	return &fe
}

type FEStruct struct {
	*FEType

	ID string
	Documentation
	Fields   []*FEField
	original *scanner.Struct
}

type Documentation struct {
	Docs     []string `json:",omitempty"`
	Comments []string `json:",omitempty"`
}

func getDocumentation(docs scanner.Docs) Documentation {
	out := Documentation{}
	if docs.Doc != nil {
		out.Docs = docs.Doc
	}
	if docs.Comment != nil {
		out.Comments = docs.Comment
	}
	return out
}

type FEField struct {
	*FEType
	ID string
	Documentation
}

func (v *FEStruct) GetOriginal() *scanner.Struct {
	return v.original
}

// RelativeTo returns a Qualifier that fully qualifies members of
// all packages other than pkg.
func RelativeTo(pkgPath string) types.Qualifier {
	if pkgPath == "" {
		return nil
	}
	return func(other *types.Package) string {
		if pkgPath == other.Path() {
			return "" // same package; unqualified
		}
		return other.Path()
	}
}
