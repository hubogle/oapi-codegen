// Copyright 2019 DeepMap, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package codegen

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime/debug"
	"sort"
	"strings"
	"text/template"

	"github.com/deepmap/oapi-codegen/pkg/util"
	"github.com/getkin/kin-openapi/openapi3"
	"golang.org/x/tools/imports"
)

// Embed the templates directory
//
//go:embed templates
var templates embed.FS

// globalState stores all global state. Please don't put global state anywhere
// else so that we can easily track it.
var globalState struct {
	options Configuration
	spec    *openapi3.T
}

// goImport represents a go package to be imported in the generated code
type goImport struct {
	Name string // package name
	Path string // package path
}

// String returns a go import statement
func (gi goImport) String() string {
	if gi.Name != "" {
		return fmt.Sprintf("%s %q", gi.Name, gi.Path)
	}
	return fmt.Sprintf("%q", gi.Path)
}

// importMap maps external OpenAPI specifications files/urls to external go packages
type importMap map[string]goImport

// GoImports returns a slice of go import statements
func (im importMap) GoImports() []string {
	goImports := make([]string, 0, len(im))
	for _, v := range im {
		goImports = append(goImports, v.String())
	}
	return goImports
}

var importMapping importMap

func constructImportMapping(importMapping map[string]string) importMap {
	var (
		pathToName = map[string]string{}
		result     = importMap{}
	)

	{
		var packagePaths []string
		for _, packageName := range importMapping {
			packagePaths = append(packagePaths, packageName)
		}
		sort.Strings(packagePaths)

		for _, packagePath := range packagePaths {
			if _, ok := pathToName[packagePath]; !ok {
				pathToName[packagePath] = fmt.Sprintf("externalRef%d", len(pathToName))
			}
		}
	}
	for specPath, packagePath := range importMapping {
		result[specPath] = goImport{Name: pathToName[packagePath], Path: packagePath}
	}
	return result
}

// Generate uses the Go templating engine to generate all of our server wrappers from
// the descriptions we've built up above from the schema objects.
// opts defines
func Generate(spec *openapi3.T, opts Configuration) (string, error) {
	// This is global state
	globalState.options = opts
	globalState.spec = spec

	importMapping = constructImportMapping(opts.ImportMapping)

	filterOperationsByTag(spec, opts)
	if !opts.OutputOptions.SkipPrune {
		pruneUnusedComponents(spec)
	}

	// if we are provided an override for the response type suffix update it
	if opts.OutputOptions.ResponseTypeSuffix != "" {
		responseTypeSuffix = opts.OutputOptions.ResponseTypeSuffix
	}

	if globalState.options.OutputOptions.ClientTypeName == "" {
		globalState.options.OutputOptions.ClientTypeName = defaultClientTypeName
	}

	// This creates the golang templates text package
	TemplateFunctions["opts"] = func() Configuration { return globalState.options }
	t := template.New("oapi-codegen").Funcs(TemplateFunctions)
	// This parses all of our own template files into the template object
	// above
	err := LoadTemplates(templates, t)
	if err != nil {
		return "", fmt.Errorf("error parsing oapi-codegen templates: %w", err)
	}

	// Override built-in templates with user-provided versions
	for _, tpl := range t.Templates() {
		if _, ok := opts.OutputOptions.UserTemplates[tpl.Name()]; ok {
			utpl := t.New(tpl.Name())
			if _, err := utpl.Parse(opts.OutputOptions.UserTemplates[tpl.Name()]); err != nil {
				return "", fmt.Errorf("error parsing user-provided template %q: %w", tpl.Name(), err)
			}
		}
	}

	groupOps, err := OperationDefinitions(spec)
	if err != nil {
		return "", fmt.Errorf("error creating operation definitions: %w", err)
	}
	// TODO: handler，logic，routes，types，middleware 四个文件夹，内容按照 group 进行 进行分组
	middlewareMap := make(map[string]struct{})
	groupNameList := []string{}
	allops := make([]OperationDefinition, 0)
	includeSchemas := []string{}

	for groupName, ops := range groupOps {
		allops = append(allops, ops...)
		// models 整体生成一个文件
		var typeDefinitions, constantDefinitions string
		var xGoTypeImports map[string]goImport
		groupNameList = append(groupNameList, groupName)
		if opts.Generate.Models {
			xGoTypeImports, err := OperationImports(ops)
			if err != nil {
				return "", fmt.Errorf("error getting operation imports: %w", err)
			}
			opsRequestMap := make(map[string]struct{})
			opsResponseMap := make(map[string]struct{})
			for _, op := range ops {
				if op.Bodies != nil {
					for _, body := range op.Bodies {
						if body.Schema.RefType != "" {
							opsRequestMap[body.Schema.RefType] = struct{}{}
							includeSchemas = append(includeSchemas, body.Schema.RefType)
						}
					}
				}
				if op.Responses != nil {
					for _, resp := range op.Responses {
						if resp.StatusCode == "200" {
							if resp.Contents != nil && resp.Contents[0].Schema.RefType != "" {
								opsResponseMap[resp.Contents[0].Schema.RefType] = struct{}{}
								includeSchemas = append(includeSchemas, resp.Contents[0].Schema.RefType)
							}
						}
					}
				}
			}
			// 传递需要的 ref 和需要解析的 groupName
			typeDefinitions, err = GenerateTypeDefinitions(t, spec, ops, opts.OutputOptions.ExcludeSchemas, true, opsResponseMap, opsRequestMap, includeSchemas)
			if err != nil {
				return "", fmt.Errorf("error generating type definitions: %w", err)
			}

			constantDefinitions, err = GenerateConstants(t, ops)
			if err != nil {
				return "", fmt.Errorf("error generating constants: %w", err)
			}

			improts, err := GetTypeDefinitionsImports(spec, opts.OutputOptions.ExcludeSchemas)
			if err != nil {
				return "", fmt.Errorf("error getting type definition imports: %w", err)
			}
			MergeImports(xGoTypeImports, improts)
		}

		// 输出 types 定义的字段
		if opts.Generate.Models && opts.OutputDirOptions.TypesDir != "" {
			externalImports := append(importMapping.GoImports(), importMap(xGoTypeImports).GoImports()...)
			externalImports = append(externalImports, `"`+path.Join(opts.PackageName, opts.OutputDirOptions.TypesDir)+`"`)
			importsOut, err := GenerateImports(t, externalImports, groupName)
			if err != nil {
				return "", err
			}
			fpath := path.Join(opts.OutputDirOptions.TypesDir, groupName, "types.go")
			os.Remove(fpath)
			err = OutputFile(groupName, opts.OutputDirOptions.TypesDir, "types.go", []string{importsOut, constantDefinitions, typeDefinitions})
			if err != nil {
				return "", err
			}
		}

		// 输出 routes 代码定义
		if opts.OutputDirOptions.RoutesDir != "" {
			if err = OutPutRoutesCode(groupName, ops, opts, t); err != nil {
				return "", err
			}
		}

		// 遍历输出 logic handler 模版文件
		for _, op := range ops {
			// 输出 logic 资源定义
			if err = OutPutLogicCode(groupName, op, opts, t); err != nil {
				return "", err
			}

			// 输出 handler 资源定义
			if err = OutPutHandlerCode(groupName, op, opts, t); err != nil {
				return "", err
			}
			for _, name := range op.ExtMiddleware {
				middlewareMap[name] = struct{}{}
			}
		}

	}
	// 输出 types 定义的字段
	if opts.Generate.Models && opts.OutputDirOptions.TypesDir != "" {
		importsOut, err := GenerateImports(t, []string{}, "types")
		if err != nil {
			return "", err
		}
		typeDefinitions, err := GenerateTypeDefinitions(t, spec, allops, includeSchemas, false, nil, nil, nil)
		fpath := path.Join(opts.OutputDirOptions.TypesDir, "types.go")
		os.Remove(fpath)
		err = OutputFile("", opts.OutputDirOptions.TypesDir, "types.go", []string{importsOut, typeDefinitions})
		if err != nil {
			return "", err
		}
	}
	// 输出定义的 Code 代码
	if opts.OutputDirOptions.CodeDir != "" {
		ginCodeOut, _ := GenerateGinCode(t)
		err = OutputFile("", opts.OutputDirOptions.CodeDir, "code.go", []string{ginCodeOut})
		if err != nil {
			return "", err
		}
	}
	// 输出定义的 Response 代码
	if opts.OutputDirOptions.ResponseDir != "" {
		importPkgName := `"` + path.Join(opts.PackageName, opts.OutputDirOptions.CodeDir) + `"`
		responseOp := GinOperation{
			ImportPkgName: importPkgName,
		}
		ginResponseOut, _ := GenerateGinResponse(t, &responseOp)
		err = OutputFile("", opts.OutputDirOptions.ResponseDir, "response.go", []string{ginResponseOut})
		if err != nil {
			return "", err
		}
	}
	// 输出 svc 资源定义
	if opts.OutputDirOptions.SvcDir != "" {
		ginSvcOut, err := GenerateGinSvc(t)
		if err != nil {
			return "", fmt.Errorf("error generating svc for Paths: %w", err)
		}
		err = OutputFile("", opts.OutputDirOptions.SvcDir, "servicecontext.go", []string{ginSvcOut})
		if err != nil {
			return "", err
		}
	}
	// 输出 middleware 代码
	if err = OutPutMiddlewareCode(middlewareMap, opts, t); err != nil {
		return "", err
	}

	// 输出主路由定义代码
	if err = OutPutRoutesSetupCode(groupNameList, opts, t); err != nil {
		return "", err
	}

	return "", nil
}

// OutPutMiddlewareCode 中间件逻辑生成
func OutPutMiddlewareCode(middlewareMap map[string]struct{}, opts Configuration, t *template.Template) error {
	for name := range middlewareMap {
		if name == "" {
			continue
		}
		middlewareOut, err := GenerateGinMiddleware(t, name)
		if err != nil {
			return err
		}
		fileName := fmt.Sprintf("%s_middleware.go", name)
		err = OutputFile("", opts.OutputDirOptions.MiddlewareDir, fileName, []string{middlewareOut})
		if err != nil {
			return err
		}
	}
	return nil
}

// OutPutRoutesCode 构造生成 routes 的逻辑代码
func OutPutRoutesCode(groupName string, ops []OperationDefinition, opts Configuration, t *template.Template) error {
	importPkgName := ""
	importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.SvcDir) + `"` + "\n"
	importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.MiddlewareDir) + `"` + "\n"
	importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.HandlerDir, groupName) + `"` + "\n"
	routesOp := GinRoutesOperation{
		Ops:           ops,
		PkgName:       groupName,
		ImportPkgName: importPkgName,
	}
	routesOut, err := GenerateGinRoutes(t, &routesOp)
	if err != nil {
		return fmt.Errorf("error generating logic for Paths: %w", err)
	}
	fileName := fmt.Sprintf("%s.go", groupName)
	fpath := path.Join(opts.OutputDirOptions.RoutesDir, groupName, fileName)
	os.Remove(fpath)
	err = OutputFile(groupName, opts.OutputDirOptions.RoutesDir, fileName, []string{routesOut})
	if err != nil {
		return err
	}

	return nil
}

// OutPutRoutesSetupCode 构造生成 routes setup 代码
func OutPutRoutesSetupCode(groupNameList []string, opts Configuration, t *template.Template) error {

	importPkgName := ""
	for _, groupName := range groupNameList {
		importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.RoutesDir, groupName) + `"` + "\n"
	}
	routesSetupOp := GinRoutesOperation{
		// PkgName:
		ImportPkgName: importPkgName,
		GroupNameList: groupNameList,
	}
	routesSetupOut, _ := GenerateGinRoutesSetup(t, &routesSetupOp)
	fpath := path.Join(opts.OutputDirOptions.RoutesDir, "", "routes.go")
	os.Remove(fpath)
	err := OutputFile("", opts.OutputDirOptions.RoutesDir, "routes.go", []string{routesSetupOut})
	if err != nil {
		return err
	}
	return nil
}

// OutPutLogicCode 构造生成 logic 的逻辑代码
func OutPutLogicCode(groupName string, op OperationDefinition, opts Configuration, t *template.Template) error {
	if opts.OutputDirOptions.LogicDir == "" {
		return nil
	}
	var reqName = ""
	var respName = ""
	var importPkgName = ""
	if len(op.Bodies) > 0 {
		op.BodyRequired = true
		// TODO 当参数有多个 params 和 body 时需要解析
		if op.Bodies[0].Schema.RefType != "" { // 当 yaml 没用通过 $ref 引用定义变量时
			reqName = fmt.Sprintf("*%s.%s", groupName, op.Bodies[0].Schema.RefType)
		} else {
			reqName = fmt.Sprintf("*%s.%s", groupName, op.Bodies[0].Schema.GoType)
		}
	}
	if len(op.Responses) > 0 && op.Responses[0].Contents != nil {
		// TODO 只用状态码 200 定义的
		if op.Responses[0].Contents[0].Schema.RefType != "" {
			respName = fmt.Sprintf("%s.%s", groupName, op.Responses[0].Contents[0].Schema.RefType)
		} else if op.Responses[0].Contents[0].Schema.GoType != "" {
			respName = fmt.Sprintf("%s.%s", groupName, op.Responses[0].Contents[0].Schema.GoType)
		}
		if respName == "" {
			op.Responses = nil
		}
	}
	importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.SvcDir) + `"`
	if reqName != "" && respName != "" {
		importPkgName += "\n"
		importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.TypesDir, groupName) + `"`
	}
	logicOp := GinOperation{
		OperationDefinition: op,
		PkgName:             groupName,
		ImportPkgName:       importPkgName,
		ReqName:             reqName,
		RespName:            respName,
	}
	logicOut, err := GenerateGinLogic(t, &logicOp)
	if err != nil {
		return fmt.Errorf("error generating logic for Paths: %w", err)
	}
	fileName := fmt.Sprintf("%s_logic.go", CamelToSnake(op.OperationId))
	err = OutputFile(groupName, opts.OutputDirOptions.LogicDir, fileName, []string{logicOut})
	if err != nil {
		return err
	}
	return nil
}

// OutPutHandlerCode 构造生成 handler 逻辑代码
func OutPutHandlerCode(groupName string, op OperationDefinition, opts Configuration, t *template.Template) error {
	if opts.OutputDirOptions.HandlerDir == "" {
		return nil
	}
	var reqName = ""
	var importPkgName = ""
	var shouldBindStr = ""
	importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.SvcDir) + `"`
	if len(op.Bodies) > 0 {
		// TODO 当参数有多个 params 和 body 时需要解析
		if op.Bodies[0].Schema.RefType != "" { // 当 yaml 没用通过 $ref 引用定义变量时
			reqName = fmt.Sprintf("*%sType.%s", groupName, op.Bodies[0].Schema.RefType)
		} else {
			reqName = fmt.Sprintf("*%sType.%s", groupName, op.Bodies[0].Schema.GoType)
		}
		importPkgName += "\n"
		importPkgName += fmt.Sprintf("%sType ", groupName)
		importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.TypesDir, groupName) + `"`
		importPkgName += "\n"
		importPkgName += `"` + path.Join(opts.PackageName, opts.OutputDirOptions.ResponseDir) + `"`
	}
	shouldBindMethod := make(map[string]struct{})
	for _, paras := range op.AllParams() {
		if paras.In == "path" {
			shouldBindMethod["Uri"] = struct{}{}
		}
		if paras.In == "query" {
			shouldBindMethod["Query"] = struct{}{}
		}
		if paras.In == "header" {
			shouldBindMethod["Header"] = struct{}{}
		}
	}
	// TODO 从参数解析需要用哪种方式绑定参数 ShouldBindUri, ShouldBindJSON
	for _, parse := range op.Bodies {
		if parse.ContentType == "application/json" {
			shouldBindMethod["JSON"] = struct{}{}
		}
	}
	for key := range shouldBindMethod {
		shouldBindStr += fmt.Sprintf(`    if err := c.%s(&%s); err != nil {
			response.HandlerParamsResponse(c, err)
			return
		}`, "ShouldBind"+key, "req") + "\n"
	}
	handlerOp := GinOperation{
		OperationDefinition: op,
		PkgName:             groupName,
		ShouldBindStr:       shouldBindStr,
		ImportPkgName:       importPkgName,
		ReqName:             reqName,
	}
	handlerOut, err := GenerateGinHandler(t, &handlerOp)
	if err != nil {
		return fmt.Errorf("error generating handler for Paths: %w", err)
	}
	fileName := fmt.Sprintf("%s_handler.go", CamelToSnake(op.OperationId))
	err = OutputFile(groupName, opts.OutputDirOptions.HandlerDir, fileName, []string{handlerOut})
	if err != nil {
		return err
	}
	return nil
}

// OutputFile 输出生成的代码文件
func OutputFile(groupName string, dirName string, fileName string, contextList []string) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	fp, created, err := util.MaybeCreateFile(dirName, groupName, fileName)
	if err != nil || !created {
		return err
	}
	for _, context := range contextList {
		_, err = w.WriteString(context)
		if err != nil {
			return err
		}
	}
	err = w.Flush()
	if err != nil {
		return fmt.Errorf("error flushing output buffer: %w", err)
	}
	goCode := SanitizeCode(buf.String())
	fpath := path.Join(dirName, groupName, fileName)
	outBytes, err := imports.Process(fpath, []byte(goCode), nil)
	if err != nil {
		return fmt.Errorf("error formatting Go code %s: %w", goCode, err)
	}
	fp.Write(outBytes)
	return nil
}

// 检查 type 是否被用到
func checkParamUse(types *[]TypeDefinition, ops []OperationDefinition) []TypeDefinition {
	opsMap := map[string]string{}              // 所有使用的 ops
	typeNameMap := map[string]TypeDefinition{} // 使用 TypeName 映射对应的对象
	resultTypes := map[string]TypeDefinition{} // 当前 op 用到的 type 对象
	for _, op := range ops {
		if op.PathParams != nil {
			for _, bo := range op.PathParams {
				opsMap[bo.Schema.GoType] = bo.Schema.GoType
				if bo.Schema.RefType != "" {
					opsMap[bo.Schema.RefType] = bo.Schema.GoType
				}
			}
		}
		if op.Bodies != nil {
			for _, bo := range op.Bodies {
				opsMap[bo.Schema.GoType] = bo.Schema.GoType
				if bo.Schema.RefType != "" {
					opsMap[bo.Schema.RefType] = bo.Schema.GoType
				}
			}
		}
		if op.QueryParams != nil {
			for _, bo := range op.QueryParams {
				opsMap[bo.Schema.GoType] = bo.Schema.GoType
				if bo.Schema.RefType != "" {
					opsMap[bo.Schema.RefType] = bo.Schema.GoType
				}
			}
		}
		if op.Responses != nil {
			for _, resp := range op.Responses {
				if resp.StatusCode == "200" {
					if strings.HasPrefix(resp.Contents[0].Schema.GoType, "struct {") {
						for _, pro := range resp.Contents[0].Schema.Properties {
							opsMap[pro.Schema.GoType] = pro.Schema.GoType
						}
					} else {
						opsMap[resp.Contents[0].Schema.GoType] = resp.Contents[0].Schema.GoType
					}
				}
			}
		}
	}
	for _, typeDef := range *types {
		typeNameMap[typeDef.TypeName] = typeDef
	}

	for name := range opsMap {
		if val, ok := typeNameMap[name]; ok {
			resultTypes[name] = val
			if val.Schema.Properties != nil {
				typeNameList := []string{}
				for _, pro := range val.Schema.Properties {
					GetProperties(pro, &typeNameList)
				}
				for _, typeName := range typeNameList {
					if v, ok := typeNameMap[typeName]; ok {
						resultTypes[typeName] = v
					}
				}
			}
		}
	}

	result := make([]TypeDefinition, 0)
	for _, t := range *types {
		if _, ok := resultTypes[t.TypeName]; ok {
			result = append(result, t)
		}

	}
	return result
}

// GetProperties 递归获取字段包含的 properties
func GetProperties(pro Property, jsonFieldNameList *[]string) {
	if pro.JsonFieldName != "" {
		*jsonFieldNameList = append(*jsonFieldNameList, ToCamelCase(pro.Schema.RefType))
	}
	if pro.Schema.Properties != nil {
		for _, subPro := range pro.Schema.Properties {
			GetProperties(subPro, jsonFieldNameList)
		}
	}
	if pro.Schema.ArrayType != nil {
		at := pro.Schema.ArrayType
		if at.GoType != "" {
			*jsonFieldNameList = append(*jsonFieldNameList, at.GoType)
		}
	}
}

func GenerateTypeDefinitions(t *template.Template, swagger *openapi3.T, ops []OperationDefinition, excludeSchemas []string, flag bool, opsResponseMap, opsRequestMap map[string]struct{}, includeSchemasMap []string) (string, error) {
	var allTypes []TypeDefinition
	if swagger.Components != nil {
		responseTypes, err := GenerateTypesForResponses(t, swagger.Components.Responses, opsResponseMap)
		if err != nil {
			return "", fmt.Errorf("error generating Go types for component responses: %w", err)
		}

		bodyTypes, err := GenerateTypesForRequestBodies(t, swagger.Components.RequestBodies, opsRequestMap)
		if err != nil {
			return "", fmt.Errorf("error generating Go types for component request bodies: %w", err)
		}

		schemaTypes, err := GenerateTypesForSchemas(t, swagger.Components.Schemas, excludeSchemas, nil)
		if err != nil {
			return "", fmt.Errorf("error generating Go types for component schemas: %w", err)
		}

		paramTypes, err := GenerateTypesForParameters(t, swagger.Components.Parameters)
		if err != nil {
			return "", fmt.Errorf("error generating Go types for component parameters: %w", err)
		}
		if flag {
			schemaTypes, err = GenerateTypesForSchemas(t, swagger.Components.Schemas, excludeSchemas, includeSchemasMap)
			if err != nil {
				return "", fmt.Errorf("error generating Go types for component schemas: %w", err)
			}
			allTypes = append(responseTypes, bodyTypes...)
			allTypes = append(allTypes, schemaTypes...)
		} else {
			allTypes = append(schemaTypes, paramTypes...)
		}

		// TODO 判断 schema 是否被使用，否则的话就不添加，深度引用的时候还不行
		// allTypes = checkParamUse(&allTypes, ops)
	}

	// Go through all operations, and add their types to allTypes, so that we can
	// scan all of them for enums. Operation definitions are handled differently
	// from the rest, so let's keep track of enumTypes separately, which will contain
	// all types needed to be scanned for enums, which includes those within operations.
	enumTypes := allTypes
	for _, op := range ops {
		enumTypes = append(enumTypes, op.TypeDefinitions...)
	}

	operationsOut, err := GenerateTypesForOperations(t, ops)
	if err != nil {
		return "", fmt.Errorf("error generating Go types for component request bodies: %w", err)
	}

	enumsOut, err := GenerateEnums(t, enumTypes)
	if err != nil {
		return "", fmt.Errorf("error generating code for type enums: %w", err)
	}

	typesOut, err := GenerateTypes(t, allTypes)
	if err != nil {
		return "", fmt.Errorf("error generating code for type definitions: %w", err)
	}

	allOfBoilerplate, err := GenerateAdditionalPropertyBoilerplate(t, allTypes)
	if err != nil {
		return "", fmt.Errorf("error generating allOf boilerplate: %w", err)
	}

	unionBoilerplate, err := GenerateUnionBoilerplate(t, allTypes)
	if err != nil {
		return "", fmt.Errorf("error generating union boilerplate: %w", err)
	}

	unionAndAdditionalBoilerplate, err := GenerateUnionAndAdditionalProopertiesBoilerplate(t, allTypes)
	if err != nil {
		return "", fmt.Errorf("error generating boilerplate for union types with additionalProperties: %w", err)
	}
	var typeDefinitions string
	if flag { // 只需要请求体 req 和 resp
		typeDefinitions = strings.Join([]string{typesOut, operationsOut, allOfBoilerplate, unionBoilerplate, unionAndAdditionalBoilerplate}, "")
	} else {

		typeDefinitions = strings.Join([]string{enumsOut, typesOut}, "")
	}

	return typeDefinitions, nil
}

// GenerateConstants generates operation ids, context keys, paths, etc. to be exported as constants
func GenerateConstants(t *template.Template, ops []OperationDefinition) (string, error) {
	constants := Constants{
		SecuritySchemeProviderNames: []string{},
	}

	providerNameMap := map[string]struct{}{}
	for _, op := range ops {
		for _, def := range op.SecurityDefinitions {
			providerName := SanitizeGoIdentity(def.ProviderName)
			providerNameMap[providerName] = struct{}{}
		}
	}

	var providerNames []string
	for providerName := range providerNameMap {
		providerNames = append(providerNames, providerName)
	}

	sort.Strings(providerNames)

	constants.SecuritySchemeProviderNames = append(constants.SecuritySchemeProviderNames, providerNames...)

	return GenerateTemplates([]string{"constants.tmpl"}, t, constants)
}

// GenerateTypesForSchemas generates type definitions for any custom types defined in the
// components/schemas section of the Swagger spec.
func GenerateTypesForSchemas(t *template.Template, schemas map[string]*openapi3.SchemaRef, excludeSchemas []string, includeSchemas []string) ([]TypeDefinition, error) {
	excludeSchemasMap := make(map[string]bool)
	includeSchemasMap := make(map[string]bool)
	for _, schema := range excludeSchemas {
		excludeSchemasMap[schema] = true
	}
	for _, schema := range includeSchemas {
		includeSchemasMap[schema] = true
	}

	types := make([]TypeDefinition, 0)
	// We're going to define Go types for every object under components/schemas
	for _, schemaName := range SortedSchemaKeys(schemas) {
		if _, ok := excludeSchemasMap[schemaName]; ok {
			continue
		}
		if includeSchemas != nil {
			if _, ok := includeSchemasMap[schemaName]; !ok {
				continue
			}
		}
		schemaRef := schemas[schemaName]

		goSchema, err := GenerateGoSchema(schemaRef, []string{schemaName})
		if err != nil {
			return nil, fmt.Errorf("error converting Schema %s to Go type: %w", schemaName, err)
		}

		goTypeName, err := renameSchema(schemaName, schemaRef)
		if err != nil {
			return nil, fmt.Errorf("error making name for components/schemas/%s: %w", schemaName, err)
		}
		if goSchema.Description == "" && schemaRef.Value.Description != "" {
			goSchema.Description = schemaRef.Value.Description
		}

		types = append(types, TypeDefinition{
			JsonName: schemaName,
			TypeName: goTypeName,
			Schema:   goSchema,
		})

		types = append(types, goSchema.GetAdditionalTypeDefs()...)
	}
	return types, nil
}

// GenerateTypesForParameters generates type definitions for any custom types defined in the
// components/parameters section of the Swagger spec.
func GenerateTypesForParameters(t *template.Template, params map[string]*openapi3.ParameterRef) ([]TypeDefinition, error) {
	var types []TypeDefinition
	for _, paramName := range SortedParameterKeys(params) {
		paramOrRef := params[paramName]

		goType, err := paramToGoType(paramOrRef.Value, nil)
		if err != nil {
			return nil, fmt.Errorf("error generating Go type for schema in parameter %s: %w", paramName, err)
		}

		goTypeName, err := renameParameter(paramName, paramOrRef)
		if err != nil {
			return nil, fmt.Errorf("error making name for components/parameters/%s: %w", paramName, err)
		}

		typeDef := TypeDefinition{
			JsonName: paramName,
			Schema:   goType,
			TypeName: goTypeName,
		}

		if paramOrRef.Ref != "" {
			// Generate a reference type for referenced parameters
			refType, err := RefPathToGoType(paramOrRef.Ref)
			if err != nil {
				return nil, fmt.Errorf("error generating Go type for (%s) in parameter %s: %w", paramOrRef.Ref, paramName, err)
			}
			typeDef.TypeName = SchemaNameToTypeName(refType)
		}

		types = append(types, typeDef)
	}
	return types, nil
}

// GenerateTypesForResponses generates type definitions for any custom types defined in the
// components/responses section of the Swagger spec.
func GenerateTypesForResponses(t *template.Template, responses openapi3.Responses, opsResponseMap map[string]struct{}) ([]TypeDefinition, error) {
	var types []TypeDefinition

	for _, responseName := range SortedResponsesKeys(responses) {
		if _, ok := opsResponseMap[responseName]; !ok {
			continue
		}
		responseOrRef := responses[responseName]

		// We have to generate the response object. We're only going to
		// handle application/json media types here. Other responses should
		// simply be specified as strings or byte arrays.
		response := responseOrRef.Value
		jsonResponse, found := response.Content["application/json"]
		if found {
			goType, err := GenerateGoSchema(jsonResponse.Schema, []string{responseName})
			if err != nil {
				return nil, fmt.Errorf("error generating Go type for schema in response %s: %w", responseName, err)
			}
			if goType.Description == "" && *response.Description != "" {
				goType.Description = *response.Description
			}

			goTypeName, err := renameResponse(responseName, responseOrRef)
			if err != nil {
				return nil, fmt.Errorf("error making name for components/responses/%s: %w", responseName, err)
			}

			typeDef := TypeDefinition{
				JsonName: responseName,
				Schema:   goType,
				TypeName: goTypeName,
			}

			if responseOrRef.Ref != "" {
				// Generate a reference type for referenced parameters
				refType, err := RefPathToGoType(responseOrRef.Ref)
				if err != nil {
					return nil, fmt.Errorf("error generating Go type for (%s) in parameter %s: %w", responseOrRef.Ref, responseName, err)
				}
				typeDef.TypeName = SchemaNameToTypeName(refType)
			}
			types = append(types, typeDef)
		}
	}
	return types, nil
}

// GenerateTypesForRequestBodies generates type definitions for any custom types defined in the
// components/requestBodies section of the Swagger spec.
func GenerateTypesForRequestBodies(t *template.Template, bodies map[string]*openapi3.RequestBodyRef, opsRequestMap map[string]struct{}) ([]TypeDefinition, error) {
	var types []TypeDefinition

	for _, requestBodyName := range SortedRequestBodyKeys(bodies) {
		if _, ok := opsRequestMap[requestBodyName]; !ok {
			continue
		}
		requestBodyRef := bodies[requestBodyName]

		// As for responses, we will only generate Go code for JSON bodies,
		// the other body formats are up to the user.
		response := requestBodyRef.Value
		jsonBody, found := response.Content["application/json"]
		if found {
			goType, err := GenerateGoSchema(jsonBody.Schema, []string{requestBodyName})
			if err != nil {
				return nil, fmt.Errorf("error generating Go type for schema in body %s: %w", requestBodyName, err)
			}

			goTypeName, err := renameRequestBody(requestBodyName, requestBodyRef)
			if err != nil {
				return nil, fmt.Errorf("error making name for components/schemas/%s: %w", requestBodyName, err)
			}
			if !strings.HasPrefix(goType.GoType, "struct") {
				goType.GoType = "types." + goType.GoType
			}
			typeDef := TypeDefinition{
				JsonName: requestBodyName,
				Schema:   goType,
				TypeName: goTypeName,
			}

			if requestBodyRef.Ref != "" {
				// Generate a reference type for referenced bodies
				refType, err := RefPathToGoType(requestBodyRef.Ref)
				if err != nil {
					return nil, fmt.Errorf("error generating Go type for (%s) in body %s: %w", requestBodyRef.Ref, requestBodyName, err)
				}
				typeDef.TypeName = SchemaNameToTypeName(refType)
			}
			types = append(types, typeDef)
		}
	}
	return types, nil
}

// GenerateTypes passes a bunch of types to the template engine, and buffers
// its output into a string.
func GenerateTypes(t *template.Template, types []TypeDefinition) (string, error) {
	m := map[string]TypeDefinition{}
	var ts []TypeDefinition

	for _, typ := range types {
		if prevType, found := m[typ.TypeName]; found {
			// If type names collide, we need to see if they refer to the same
			// exact type definition, in which case, we can de-dupe. If they don't
			// match, we error out.
			if TypeDefinitionsEquivalent(prevType, typ) {
				continue
			}
			// We want to create an error when we try to define the same type twice.
			// return "", fmt.Errorf("duplicate typename '%s' detected, can't auto-rename, "+
			// 	"please use x-go-name to specify your own name for one of them", typ.TypeName)
		}

		m[typ.TypeName] = typ

		ts = append(ts, typ)
	}

	context := struct {
		Types []TypeDefinition
	}{
		Types: ts,
	}

	return GenerateTemplates([]string{"typedef.tmpl"}, t, context)
}

func GenerateEnums(t *template.Template, types []TypeDefinition) (string, error) {
	enums := []EnumDefinition{}

	// Keep track of which enums we've generated
	m := map[string]bool{}

	// These are all types defined globally
	for _, tp := range types {
		if found := m[tp.TypeName]; found {
			continue
		}

		m[tp.TypeName] = true

		if len(tp.Schema.EnumValues) > 0 {
			wrapper := ""
			if tp.Schema.GoType == "string" {
				wrapper = `"`
			}
			enums = append(enums, EnumDefinition{
				Schema:         tp.Schema,
				TypeName:       tp.TypeName,
				ValueWrapper:   wrapper,
				PrefixTypeName: globalState.options.Compatibility.AlwaysPrefixEnumValues,
			})
		}
	}

	// Now, go through all the enums, and figure out if we have conflicts with
	// any others.
	for i := range enums {
		// Look through all other enums not compared so far. Make sure we don't
		// compare against self.
		e1 := enums[i]
		for j := i + 1; j < len(enums); j++ {
			e2 := enums[j]

			for e1key := range e1.GetValues() {
				_, found := e2.GetValues()[e1key]
				if found {
					e1.PrefixTypeName = true
					e2.PrefixTypeName = true
					enums[i] = e1
					enums[j] = e2
					break
				}
			}
		}

		// now see if this enum conflicts with any global type names.
		for _, tp := range types {
			// Skip over enums, since we've handled those above.
			if len(tp.Schema.EnumValues) > 0 {
				continue
			}
			_, found := e1.Schema.EnumValues[tp.TypeName]
			if found {
				e1.PrefixTypeName = true
				enums[i] = e1
			}
		}

		// Another edge case is that an enum value can conflict with its own
		// type name.
		_, found := e1.GetValues()[e1.TypeName]
		if found {
			e1.PrefixTypeName = true
			enums[i] = e1
		}
	}

	// Now see if enums conflict with any non-enum typenames

	return GenerateTemplates([]string{"constants.tmpl"}, t, Constants{EnumDefinitions: enums})
}

// GenerateImports generates our import statements and package definition.
func GenerateImports(t *template.Template, externalImports []string, packageName string) (string, error) {
	// Read build version for incorporating into generated files
	// Unit tests have ok=false, so we'll just use "unknown" for the
	// version if we can't read this.

	modulePath := "unknown module path"
	moduleVersion := "unknown version"
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path != "" {
			modulePath = bi.Main.Path
		}
		if bi.Main.Version != "" {
			moduleVersion = bi.Main.Version
		}
	}

	context := struct {
		ExternalImports   []string
		PackageName       string
		ModuleName        string
		Version           string
		AdditionalImports []AdditionalImport
	}{
		ExternalImports:   externalImports,
		PackageName:       packageName,
		ModuleName:        modulePath,
		Version:           moduleVersion,
		AdditionalImports: globalState.options.AdditionalImports,
	}

	return GenerateTemplates([]string{"imports.tmpl"}, t, context)
}

// GenerateAdditionalPropertyBoilerplate generates all the glue code which provides
// the API for interacting with additional properties and JSON-ification
func GenerateAdditionalPropertyBoilerplate(t *template.Template, typeDefs []TypeDefinition) (string, error) {
	var filteredTypes []TypeDefinition

	m := map[string]bool{}

	for _, t := range typeDefs {
		if found := m[t.TypeName]; found {
			continue
		}

		m[t.TypeName] = true

		if t.Schema.HasAdditionalProperties {
			filteredTypes = append(filteredTypes, t)
		}
	}

	context := struct {
		Types []TypeDefinition
	}{
		Types: filteredTypes,
	}

	return GenerateTemplates([]string{"additional-properties.tmpl"}, t, context)
}

func GenerateUnionBoilerplate(t *template.Template, typeDefs []TypeDefinition) (string, error) {
	var filteredTypes []TypeDefinition
	for _, t := range typeDefs {
		if len(t.Schema.UnionElements) != 0 {
			filteredTypes = append(filteredTypes, t)
		}
	}

	if len(filteredTypes) == 0 {
		return "", nil
	}

	context := struct {
		Types []TypeDefinition
	}{
		Types: filteredTypes,
	}

	return GenerateTemplates([]string{"union.tmpl"}, t, context)
}

func GenerateUnionAndAdditionalProopertiesBoilerplate(t *template.Template, typeDefs []TypeDefinition) (string, error) {
	var filteredTypes []TypeDefinition
	for _, t := range typeDefs {
		if len(t.Schema.UnionElements) != 0 && t.Schema.HasAdditionalProperties {
			filteredTypes = append(filteredTypes, t)
		}
	}

	if len(filteredTypes) == 0 {
		return "", nil
	}
	context := struct {
		Types []TypeDefinition
	}{
		Types: filteredTypes,
	}

	return GenerateTemplates([]string{"union-and-additional-properties.tmpl"}, t, context)
}

// SanitizeCode runs sanitizers across the generated Go code to ensure the
// generated code will be able to compile.
func SanitizeCode(goCode string) string {
	// remove any byte-order-marks which break Go-Code
	// See: https://groups.google.com/forum/#!topic/golang-nuts/OToNIPdfkks
	return strings.Replace(goCode, "\uFEFF", "", -1)
}

// LoadTemplates loads all of our template files into a text/template. The
// path of template is relative to the templates directory.
func LoadTemplates(src embed.FS, t *template.Template) error {
	return fs.WalkDir(src, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("error walking directory %s: %w", path, err)
		}
		if d.IsDir() {
			return nil
		}

		buf, err := src.ReadFile(path)
		if err != nil {
			return fmt.Errorf("error reading file '%s': %w", path, err)
		}

		templateName := strings.TrimPrefix(path, "templates/")
		tmpl := t.New(templateName)
		_, err = tmpl.Parse(string(buf))
		if err != nil {
			return fmt.Errorf("parsing template '%s': %w", path, err)
		}
		return nil
	})
}

func OperationSchemaImports(s *Schema) (map[string]goImport, error) {
	res := map[string]goImport{}

	for _, p := range s.Properties {
		imprts, err := GoSchemaImports(&openapi3.SchemaRef{Value: p.Schema.OAPISchema})
		if err != nil {
			return nil, err
		}
		MergeImports(res, imprts)
	}

	imprts, err := GoSchemaImports(&openapi3.SchemaRef{Value: s.OAPISchema})
	if err != nil {
		return nil, err
	}
	MergeImports(res, imprts)
	return res, nil
}

func OperationImports(ops []OperationDefinition) (map[string]goImport, error) {
	res := map[string]goImport{}
	for _, op := range ops {
		for _, pd := range [][]ParameterDefinition{op.PathParams, op.QueryParams} {
			for _, p := range pd {
				imprts, err := OperationSchemaImports(&p.Schema)
				if err != nil {
					return nil, err
				}
				MergeImports(res, imprts)
			}
		}

		for _, b := range op.Bodies {
			imprts, err := OperationSchemaImports(&b.Schema)
			if err != nil {
				return nil, err
			}
			MergeImports(res, imprts)
		}

		for _, b := range op.Responses {
			for _, c := range b.Contents {
				imprts, err := OperationSchemaImports(&c.Schema)
				if err != nil {
					return nil, err
				}
				MergeImports(res, imprts)
			}
		}

	}
	return res, nil
}

func GetTypeDefinitionsImports(swagger *openapi3.T, excludeSchemas []string) (map[string]goImport, error) {
	res := map[string]goImport{}
	if swagger.Components == nil {
		return res, nil
	}

	schemaImports, err := GetSchemaImports(swagger.Components.Schemas, excludeSchemas)
	if err != nil {
		return nil, err
	}

	reqBodiesImports, err := GetRequestBodiesImports(swagger.Components.RequestBodies)
	if err != nil {
		return nil, err
	}

	responsesImports, err := GetResponsesImports(swagger.Components.Responses)
	if err != nil {
		return nil, err
	}

	parametersImports, err := GetParametersImports(swagger.Components.Parameters)
	if err != nil {
		return nil, err
	}

	for _, imprts := range []map[string]goImport{schemaImports, reqBodiesImports, responsesImports, parametersImports} {
		MergeImports(res, imprts)
	}
	return res, nil
}

func GoSchemaImports(schemas ...*openapi3.SchemaRef) (map[string]goImport, error) {
	res := map[string]goImport{}
	for _, sref := range schemas {
		if sref == nil || sref.Value == nil || IsGoTypeReference(sref.Ref) {
			return nil, nil
		}
		if gi, err := ParseGoImportExtension(sref); err != nil {
			return nil, err
		} else {
			if gi != nil {
				res[gi.String()] = *gi
			}
		}
		schemaVal := sref.Value

		t := schemaVal.Type
		switch t {
		case "", "object":
			for _, v := range schemaVal.Properties {
				imprts, err := GoSchemaImports(v)
				if err != nil {
					return nil, err
				}
				MergeImports(res, imprts)
			}
		case "array":
			imprts, err := GoSchemaImports(schemaVal.Items)
			if err != nil {
				return nil, err
			}
			MergeImports(res, imprts)
		}
	}
	return res, nil
}

func GetSchemaImports(schemas map[string]*openapi3.SchemaRef, excludeSchemas []string) (map[string]goImport, error) {
	res := map[string]goImport{}
	excludeSchemasMap := make(map[string]bool)
	for _, schema := range excludeSchemas {
		excludeSchemasMap[schema] = true
	}
	for schemaName, schema := range schemas {
		if _, ok := excludeSchemasMap[schemaName]; ok {
			continue
		}

		imprts, err := GoSchemaImports(schema)
		if err != nil {
			return nil, err
		}
		MergeImports(res, imprts)
	}
	return res, nil
}

func GetRequestBodiesImports(bodies map[string]*openapi3.RequestBodyRef) (map[string]goImport, error) {
	res := map[string]goImport{}
	for _, r := range bodies {
		response := r.Value
		jsonBody, found := response.Content["application/json"]
		if found {
			imprts, err := GoSchemaImports(jsonBody.Schema)
			if err != nil {
				return nil, err
			}
			MergeImports(res, imprts)
		}
	}
	return res, nil
}

func GetResponsesImports(responses map[string]*openapi3.ResponseRef) (map[string]goImport, error) {
	res := map[string]goImport{}
	for _, r := range responses {
		response := r.Value
		jsonResponse, found := response.Content["application/json"]
		if found {
			imprts, err := GoSchemaImports(jsonResponse.Schema)
			if err != nil {
				return nil, err
			}
			MergeImports(res, imprts)
		}
	}
	return res, nil
}

func GetParametersImports(params map[string]*openapi3.ParameterRef) (map[string]goImport, error) {
	res := map[string]goImport{}
	for _, param := range params {
		if param.Value == nil {
			continue
		}
		imprts, err := GoSchemaImports(param.Value.Schema)
		if err != nil {
			return nil, err
		}
		MergeImports(res, imprts)
	}
	return res, nil
}
