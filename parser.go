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

	"github.com/gagliardetto/codebox/scanner"
	. "github.com/gagliardetto/utilz"
)

func Load(pk *scanner.Package) (*FEModule, error) {
	feModule := &FEModule{
		Funcs:            make([]*FEFunc, 0),
		TypeMethods:      make([]*FETypeMethod, 0),
		InterfaceMethods: make([]*FEInterfaceMethod, 0),
	}

	{
		feModule.ID = pk.Path
		feModule.PkgPath = scanner.RemoveGoSrcClonePath(pk.Path)
		feModule.PkgName = pk.Name

		for _, fn := range pk.Funcs {
			if fn.Receiver == nil {
				f := getFEFunc(fn)
				// TODO: what to do with aliases???
				f.PkgPath = feModule.PkgPath
				feModule.Funcs = append(feModule.Funcs, f)
			}
		}
		for _, mt := range pk.Methods {
			meth := getFETypeMethod(mt, pk.Funcs)
			if meth != nil {
				feModule.TypeMethods = append(feModule.TypeMethods, meth)
			}
		}
		for _, it := range pk.Interfaces {
			feModule.InterfaceMethods = append(feModule.InterfaceMethods, getAllFEInterfaceMethods(it)...)
		}
	}

	// Sort funcs by name:
	sort.Slice(feModule.Funcs, func(i, j int) bool {
		return feModule.Funcs[i].Name < feModule.Funcs[j].Name
	})
	// Sort type methods by receiver:
	sort.Slice(feModule.TypeMethods, func(i, j int) bool {
		// If same receiver...
		if feModule.TypeMethods[i].Receiver.QualifiedName == feModule.TypeMethods[j].Receiver.QualifiedName {
			// ... sort by func name:
			return feModule.TypeMethods[i].Func.Name < feModule.TypeMethods[j].Func.Name
		}
		return feModule.TypeMethods[i].Receiver.QualifiedName < feModule.TypeMethods[j].Receiver.QualifiedName
	})
	// Sort interface methods by receiver:
	sort.Slice(feModule.InterfaceMethods, func(i, j int) bool {
		// If same receiver...
		if feModule.InterfaceMethods[i].Receiver.QualifiedName == feModule.InterfaceMethods[j].Receiver.QualifiedName {
			// ... sort by func name:
			return feModule.InterfaceMethods[i].Func.Name < feModule.InterfaceMethods[j].Func.Name
		}
		return feModule.InterfaceMethods[i].Receiver.QualifiedName < feModule.InterfaceMethods[j].Receiver.QualifiedName
	})

	{ // Deduplicate:
		feModule.Funcs = DeduplicateSlice(feModule.Funcs, func(i int) string {
			return feModule.Funcs[i].Signature
		}).([]*FEFunc)

		feModule.TypeMethods = DeduplicateSlice(feModule.TypeMethods, func(i int) string {
			return feModule.TypeMethods[i].Func.Signature
		}).([]*FETypeMethod)

		feModule.InterfaceMethods = DeduplicateSlice(feModule.InterfaceMethods, func(i int) string {
			return feModule.InterfaceMethods[i].Func.Signature
		}).([]*FEInterfaceMethod)
	}
	return feModule, nil
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
type FEModule struct {
	PkgPath          string
	PkgName          string
	ID               string
	Funcs            []*FEFunc
	TypeMethods      []*FETypeMethod
	InterfaceMethods []*FEInterfaceMethod
}

type FEFunc struct {
	CodeQL    *CodeQlFinalVals
	ClassName string
	Signature string
	ID        string
	Docs      []string
	Name      string
	PkgPath   string
	PkgName   string

	Parameters []*FEType
	Results    []*FEType
	original   *scanner.Func
}

func (v *FEFunc) GetOriginal() *scanner.Func {
	return v.original
}

func DocsWithDefault(docs []string) []string {
	if docs == nil {
		docs = make([]string, 0)
	}
	return docs
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
	Docs      []string
	IsOnPtr   bool
	Receiver  *FEReceiver
	ID        string
	Func      *FEFunc
	original  types.Type
}

func (v *FETypeMethod) GetOriginal() types.Type {
	return v.original
}

type FEInterfaceMethod FETypeMethod

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
	Identity      CodeQlIdentity
	VarName       string
	TypeName      string
	PkgName       string
	PkgPath       string
	QualifiedName string
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

func getFEType(tp scanner.Type) *FEType {
	var fe FEType
	fe.original = tp
	varName := tp.GetTypesVar().Name()
	if varName != "" {
		fe.VarName = varName
	}
	fe.IsVariadic = tp.IsVariadic()
	fe.IsNullable = tp.IsNullable()
	fe.IsPtr = tp.IsPtr()
	fe.IsStruct = tp.IsStruct()
	fe.IsBasic = tp.IsBasic()
	if tp.IsVariadic() {
		fe.TypeString = "..." + tp.GetType().(*types.Slice).Elem().String()
	} else {
		fe.TypeString = tp.GetType().String()
	}
	fe.KindString = FormatKindString(tp.GetType())

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
			fe.TypeName = tp.TypeString()
		}
	}

	return &fe
}

func getFETypeMethod(mt *types.Selection, allFuncs []*scanner.Func) *FETypeMethod {
	var fe FETypeMethod

	fe.CodeQL = NewCodeQlFinalVals()
	fe.Docs = make([]string, 0)

	fe.Receiver = &FEReceiver{}
	fe.Receiver.Identity = CodeQlIdentity{
		Placeholder: "isReceiver()",
		Identity: Identity{
			Element: ElementReceiver,
			Index:   -1,
		},
	}
	fe.Receiver.TypeString = mt.Recv().String()
	fe.Receiver.KindString = FormatKindString(mt.Recv())

	{
		var named *types.Named
		ptr, isPtr := mt.Recv().(*types.Pointer)
		if isPtr {
			named = ptr.Elem().(*types.Named)
		} else {
			named = mt.Recv().(*types.Named)
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
						fe.Docs = DocsWithDefault(mtFn.Doc)
						fe.Func = getFEFunc(mtFn)
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

	fe.ID = "type-method-" + fe.Receiver.TypeName + "-" + methodFuncName
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
	fe.Receiver.Identity = CodeQlIdentity{
		Placeholder: "isReceiver()",
		Identity: Identity{
			Element: ElementReceiver,
			Index:   -1,
		},
	}

	feFunc := getFEFunc(methodFunc)
	feFunc.CodeQL = nil
	{
		fe.Receiver.original = it.GetType()
		fe.Receiver.TypeName = it.Name
		fe.Receiver.QualifiedName = scanner.StringRemoveGoPath(feFunc.PkgPath) + "." + feFunc.Name
		fe.Receiver.PkgPath = scanner.StringRemoveGoPath(feFunc.PkgPath)
		fe.Receiver.PkgName = feFunc.PkgName
	}

	fe.Func = &FEFunc{}
	methodFuncName := feFunc.Name

	{
		// Check if the method is on a pointer of a value:
		fe.IsOnPtr = true
	}
	{
		fe.Docs = DocsWithDefault(methodFunc.Doc)
		fe.Func = feFunc
	}

	fe.ID = "interface-method-" + fe.Receiver.TypeName + "-" + methodFuncName
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
				"an array %s: [%v]%s",
				t.String(),
				t.Len(),
				FormatKindString(t.Elem()),
			))
		}
	case *types.Slice:
		{
			buf.WriteString(Sf(
				"a slice %s: []%s",
				t.String(),
				FormatKindString(t.Elem()),
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
				"a map[%s]%s",
				FormatKindString(t.Key()),
				FormatKindString(t.Elem()),
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
	fe.ID = FormatCodeQlName("function-" + fn.Name)
	fe.Docs = DocsWithDefault(fn.Doc)
	fe.Signature = RemoveThisPackagePathFromSignature(fn.Signature, fn.PkgPath)
	fe.PkgPath = fn.PkgPath
	for i, in := range fn.Input {
		v := getFEType(in)

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
		v.Identity = CodeQlIdentity{
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
		v := getFEType(out)

		placeholder := Sf("isResult(%v)", i)
		if len(fn.Output) == 1 {
			placeholder = "isResult()"
		}
		v.Identity = CodeQlIdentity{
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
